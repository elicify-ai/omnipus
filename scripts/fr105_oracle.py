#!/usr/bin/env python3
"""FR-105 oracle: an INDEPENDENT evaluator of Obsidian .base files over the real
vault, used to derive the expected row set of every view.

It answers exactly one question about our importer: does an imported view return
MORE rows than the original? It is written against .base semantics directly, with
no reference to our Go code, so a shared misunderstanding cannot make both sides
agree and both be wrong.

It REFUSES rather than guesses. Any construct it does not fully understand makes
that view UNGRADEABLE and it says so. An oracle that quietly returned [] for a
construct it did not understand would turn every broadened view into a pass,
which is the precise failure this file exists to prevent.

The clock is PINNED (--today) because half these views filter on today(). An
oracle whose answer changes overnight cannot be committed as an expectation.
"""
import json, os, re, sys, yaml
from datetime import date, datetime, timedelta
from pathlib import Path

UNPARSEABLE = []

class Unsupported(Exception):
    """Not understood; the view is ungradeable. Never a silent empty result."""

# --- lexer -----------------------------------------------------------------

TOK = re.compile(r'''
    \s*(?:
      (?P<str>"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')
    | (?P<num>\d+(?:\.\d+)?)
    | (?P<op>&&|\|\||==|!=|<=|>=|[<>+\-*/(),.!])
    | (?P<ident>[A-Za-z_][A-Za-z0-9_]*)
    )''', re.X)

def lex(s):
    out, i = [], 0
    while i < len(s):
        m = TOK.match(s, i)
        if not m:
            if s[i:].strip() == '': break
            raise Unsupported(f'lex {s[i:][:20]!r}')
        i = m.end()
        for k in ('str', 'num', 'op', 'ident'):
            if m.group(k) is not None:
                out.append((k, m.group(k))); break
    out.append(('end', None))
    return out

# --- parser (recursive descent) -------------------------------------------

class P:
    def __init__(self, toks): self.t, self.i = toks, 0
    def peek(self): return self.t[self.i]
    def next(self): self.i += 1; return self.t[self.i-1]
    def accept(self, kind, val=None):
        k, v = self.peek()
        if k == kind and (val is None or v == val):
            self.next(); return True
        return False
    def expect(self, kind, val=None):
        if not self.accept(kind, val): raise Unsupported(f'expected {val or kind} at {self.peek()}')

def parse(src):
    p = P(lex(src)); n = p_or(p)
    if p.peek()[0] != 'end': raise Unsupported(f'trailing {p.peek()}')
    return n

def p_or(p):
    n = p_and(p)
    while p.accept('op', '||'): n = ('or', n, p_and(p))
    return n
def p_and(p):
    n = p_cmp(p)
    while p.accept('op', '&&'): n = ('and', n, p_cmp(p))
    return n
def p_cmp(p):
    n = p_add(p)
    for o in ('==', '!=', '<=', '>=', '<', '>'):
        if p.accept('op', o): return ('cmp', o, n, p_add(p))
    return n
def p_add(p):
    n = p_mul(p)
    while True:
        if p.accept('op', '+'):   n = ('bin', '+', n, p_mul(p))
        elif p.accept('op', '-'): n = ('bin', '-', n, p_mul(p))
        else: return n
def p_mul(p):
    n = p_post(p)
    while True:
        if p.accept('op', '*'):   n = ('bin', '*', n, p_post(p))
        elif p.accept('op', '/'): n = ('bin', '/', n, p_post(p))
        else: return n
def p_post(p):
    n = p_prim(p)
    while p.accept('op', '.'):
        k, name = p.next()
        if k != 'ident': raise Unsupported('member name')
        if p.accept('op', '('):
            args = []
            if not p.accept('op', ')'):
                args.append(p_or(p))
                while p.accept('op', ','): args.append(p_or(p))
                p.expect('op', ')')
            n = ('method', name, n, args)
        else:
            n = ('member', name, n)
    return n
def p_prim(p):
    k, v = p.next()
    if k == 'str': return ('lit', v[1:-1])
    if k == 'num': return ('lit', float(v) if '.' in v else int(v))
    if k == 'op' and v == '(':
        n = p_or(p); p.expect('op', ')'); return n
    if k == 'op' and v == '!': return ('not', p_post(p))
    if k == 'ident':
        if v in ('true', 'false'): return ('lit', v == 'true')
        if v == 'null': return ('lit', None)
        if p.accept('op', '('):
            args = []
            if not p.accept('op', ')'):
                args.append(p_or(p))
                while p.accept('op', ','): args.append(p_or(p))
                p.expect('op', ')')
            return ('call', v, args)
        return ('name', v)
    raise Unsupported(f'primary {k}:{v}')

# --- evaluation ------------------------------------------------------------

class Ctx:
    def __init__(self, rel, fm, formulas, today, backlinks):
        self.rel, self.fm, self.formulas, self.today, self.backlinks = rel, fm, formulas, today, backlinks
        self.busy = set()

def to_date(v):
    """A value that is not a date yields ABSENT (None), never an exception.

    A single note carrying the literal text "PLACEHOLDER - renewal date unknown"
    in a date field must not make the whole view ungradeable; Obsidian cannot
    compare it either, so that row simply does not match. The count of such rows
    is reported per view so the dirt stays VISIBLE rather than being absorbed.
    """
    if v is None: return None
    if isinstance(v, date) and not isinstance(v, datetime): return v
    if isinstance(v, datetime): return v.date()
    if isinstance(v, str):
        for f in ('%Y-%m-%d', '%Y/%m/%d'):
            try: return datetime.strptime(v[:10], f).date()
            except ValueError: pass
    UNPARSEABLE.append(repr(v)[:60])
    return None

def truthy(v):
    if v is None or v is False: return False
    if isinstance(v, str): return v.strip() != ''
    if isinstance(v, (list, dict)): return len(v) > 0
    return True

def ev(n, c):
    t = n[0]
    if t == 'lit':  return n[1]
    if t == 'not':  return not truthy(ev(n[1], c))
    if t == 'and':  return truthy(ev(n[1], c)) and truthy(ev(n[2], c))
    if t == 'or':   return truthy(ev(n[1], c)) or truthy(ev(n[2], c))
    if t == 'name': return lookup(n[1], c)
    if t == 'cmp':
        return cmp_op(n[1], ev(n[2], c), ev(n[3], c))
    if t == 'bin':
        a, b, o = ev(n[2], c), ev(n[3], c), n[1]
        if o == '-' and isinstance(a, date) and isinstance(b, date): return a - b
        # Arithmetic involving an ABSENT value yields ABSENT, it does not
        # error. `(date(renewal_date) - today()).days` over a note with no
        # renewal date must leave that row unmatched, not make the entire view
        # ungradeable because one note of 757 lacks a property.
        if a is None or b is None: return None
        if o == '+': return a + b
        if o == '-': return a - b
        if o == '*': return a * b
        if o == '/': return a / b if b else None
    if t == 'call':
        name, args = n[1], n[2]
        if name == 'today' or name == 'now': return c.today
        if name == 'if':
            if len(args) != 3: raise Unsupported('if arity')
            return ev(args[1], c) if truthy(ev(args[0], c)) else ev(args[2], c)
        if name == 'date': return to_date(ev(args[0], c))
        if name == 'number':
            v = ev(args[0], c)
            try: return float(v)
            except (TypeError, ValueError): return None
        raise Unsupported(f'call {name}')
    if t == 'member':
        base, name = ev(n[2], c), n[1]
        if base is None: return None          # .days / .year of absent is absent
        if isinstance(base, timedelta) and name == 'days': return base.days
        if isinstance(base, date) and name == 'year':  return base.year
        if isinstance(base, date) and name == 'month': return base.month
        if name == 'length': return len(base) if base is not None else 0
        # a dotted path that is really one property name, e.g. formula.age
        raise Unsupported(f'member .{name}')
    if t == 'method':
        name, args = n[1], n[3]
        # file.inFolder(...) / file.hasTag(...) arrive as method on name 'file'
        if n[2][0] == 'name' and n[2][1] == 'file':
            return file_method(name, [ev(a, c) for a in args], c)
        base = ev(n[2], c)
        av = [ev(a, c) for a in args]
        if name == 'contains':
            if isinstance(base, list): return av[0] in [str(x) for x in base]
            return base is not None and str(av[0]) in str(base)
        if name == 'isType':
            want = av[0]
            if want == 'number': return isinstance(base, (int, float)) and not isinstance(base, bool)
            if want == 'string': return isinstance(base, str)
            if want == 'list':   return isinstance(base, list)
            raise Unsupported(f'isType({want})')
        if name in ('startsWith', 'endsWith'):
            if base is None: return False
            return str(base).startswith(av[0]) if name == 'startsWith' else str(base).endswith(av[0])
        raise Unsupported(f'method .{name}')
    raise Unsupported(f'node {t}')

def cmp_op(o, a, b):
    # An ABSENT property compared against the empty string behaves as empty.
    # Every author in this vault writes `close_date != ""` to mean "has a
    # value", and reading absent-as-not-empty makes that filter admit exactly
    # the rows it was written to exclude. Chosen deliberately: it yields the
    # SMALLER oracle row set, so a disagreement shows up as our importer
    # returning MORE rows -- the direction that gets investigated rather than
    # the direction that hides a broadened view.
    if b == '' and a is None: a = ''
    if a == '' and b is None: b = ''
    if o == '==':
        if isinstance(a, list): return b in a
        return a == b
    if o == '!=':
        if isinstance(a, list): return b not in a
        return a != b
    if a is None or b is None or a == '' or b == '': return False
    if isinstance(a, date) and isinstance(b, str): b = to_date(b)
    if isinstance(b, date) and isinstance(a, str): a = to_date(a)
    if isinstance(a, str) != isinstance(b, str):
        try: a, b = float(a), float(b)
        except (TypeError, ValueError): a, b = str(a), str(b)
    try:
        return {'<': a < b, '<=': a <= b, '>': a > b, '>=': a >= b}[o]
    except TypeError:
        return False

def file_method(name, av, c):
    if name == 'inFolder':
        folder = str(Path(c.rel).parent)
        return folder == av[0] or folder.startswith(av[0].rstrip('/') + '/')
    if name == 'hasTag':
        tags = c.fm.get('tags') or []
        return av[0] in (tags if isinstance(tags, list) else [tags])
    if name == 'hasProperty': return av[0] in c.fm
    raise Unsupported(f'file.{name}')

def lookup(name, c):
    if name == 'file': raise Unsupported('bare file')
    if name in c.fm: return c.fm[name]
    return None

def resolve_dotted(node, c):
    """file.name / formula.x arrive as ('member', x, ('name','file'|'formula'))."""
    if node[0] == 'member' and node[2][0] == 'name':
        root, attr = node[2][1], node[1]
        if root == 'file':
            if attr == 'name':   return Path(c.rel).stem
            if attr == 'path':   return c.rel
            if attr == 'folder': return str(Path(c.rel).parent)
            if attr == 'backlinks': return c.backlinks.get(c.rel, [])
            raise Unsupported(f'file.{attr}')
        if root == 'formula':
            if attr in c.busy: raise Unsupported(f'formula cycle {attr}')
            if attr not in c.formulas: raise Unsupported(f'formula.{attr} undefined')
            c.busy.add(attr)
            try: return ev_top(c.formulas[attr], c)
            finally: c.busy.discard(attr)
    return None

_orig_ev = ev
def ev_wrapper(n, c):
    if n[0] == 'member':
        r = resolve_dotted(n, c)
        if r is not None or (n[2][0] == 'name' and n[2][1] in ('file', 'formula')):
            return r
    if n[0] == 'method' and n[2][0] == 'member':
        base = resolve_dotted(n[2], c)
        if base is not None or (n[2][2][0] == 'name' and n[2][2][1] in ('file', 'formula')):
            av = [ev(a, c) for a in n[3]]
            if n[1] == 'contains':
                if isinstance(base, list): return av[0] in [str(x) for x in base]
                return base is not None and str(av[0]) in str(base)
            raise Unsupported(f'method .{n[1]} on dotted')
    return _orig_ev(n, c)
ev = ev_wrapper

_AST = {}
def ev_top(src, c):
    if src not in _AST: _AST[src] = parse(src)
    return ev(_AST[src], c)

# --- filter trees ----------------------------------------------------------

def eval_node(node, c):
    if isinstance(node, str):  return truthy(ev_top(node, c))
    if isinstance(node, bool): return node
    if isinstance(node, list): return all(eval_node(n, c) for n in node)
    if isinstance(node, dict):
        if len(node) != 1: raise Unsupported(repr(node))
        (k, v), = node.items()
        vs = v if isinstance(v, list) else [v]
        if k == 'and': return all(eval_node(n, c) for n in vs)
        if k == 'or':  return any(eval_node(n, c) for n in vs)
        if k == 'not': return not all(eval_node(n, c) for n in vs)
        raise Unsupported(k)
    raise Unsupported(repr(node))

# --- vault -----------------------------------------------------------------

FM = re.compile(r'\A---\r?\n(.*?)\r?\n---\r?\n', re.S)
WIKILINK = re.compile(r'\[\[([^\]|#]+)')

def load_vault(root):
    notes, bodies = {}, {}
    for p in Path(root).rglob('*.md'):
        rel = str(p.relative_to(root))
        try: text = p.read_text(encoding='utf-8', errors='replace')
        except OSError: continue
        m = FM.match(text)
        fm = {}
        if m:
            try:
                parsed = yaml.safe_load(m.group(1))
                if isinstance(parsed, dict): fm = parsed
            except yaml.YAMLError: fm = {}
        notes[rel] = fm
        bodies[rel] = text
    stem = {}
    for rel in notes: stem.setdefault(Path(rel).stem, []).append(rel)
    backlinks = {rel: [] for rel in notes}
    for rel, text in bodies.items():
        for tgt in set(WIKILINK.findall(text)):
            for dest in stem.get(tgt.strip(), []):
                if dest != rel: backlinks[dest].append(rel)
    return notes, backlinks

def grade(base_path, notes, backlinks, today):
    doc = yaml.safe_load(Path(base_path).read_text(encoding='utf-8'))
    formulas = doc.get('formulas') or {}
    base_filter = doc.get('filters')
    out = {'base': os.path.basename(base_path), 'formulas': sorted(formulas), 'views': []}
    for v in doc.get('views') or []:
        e = {'name': v.get('name'), 'layout': v.get('type'),
             'has_group_by': 'groupBy' in v, 'ungradeable': None, 'rows': None}
        try:
            del UNPARSEABLE[:]
            rows = []
            for rel, fm in notes.items():
                c = Ctx(rel, fm, formulas, today, backlinks)
                if base_filter is not None and not eval_node(base_filter, c): continue
                if v.get('filters') is not None and not eval_node(v['filters'], c): continue
                rows.append(rel)
            e['rows'], e['row_count'] = sorted(rows), len(rows)
            e['unparseable_date_values'] = sorted(set(UNPARSEABLE))
        except Unsupported as ex:
            e['ungradeable'] = str(ex)
        out['views'].append(e)
    return out

def main():
    vault, dest = sys.argv[1], sys.argv[2]
    today = datetime.strptime(sys.argv[3], '%Y-%m-%d').date()
    notes, backlinks = load_vault(vault)
    bases = sorted(Path(vault).rglob('*.base'))
    rep = {'clock': str(today), 'note_count': len(notes),
           'bases': [grade(str(b), notes, backlinks, today) for b in bases]}
    Path(dest).write_text(json.dumps(rep, indent=1, sort_keys=True))
    tot = sum(len(b['views']) for b in rep['bases'])
    ung = [(b['base'], v['name'], v['ungradeable']) for b in rep['bases'] for v in b['views'] if v['ungradeable']]
    print(f"  clock pinned to    : {today}")
    print(f"  notes / bases      : {len(notes)} / {len(rep['bases'])}")
    print(f"  views              : {tot}")
    print(f"  gradeable          : {tot - len(ung)}")
    print(f"  UNGRADEABLE        : {len(ung)}")
    for b, n, why in ung: print(f"    - {b} :: {n} :: {why}")

main()

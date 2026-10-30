"""Read-only package inventory; see knowledge/inspector-contract.md for limits."""
import argparse
import json
from xml.etree import ElementTree as ET
from zipfile import BadZipFile, ZipFile

W = '{http://schemas.openxmlformats.org/wordprocessingml/2006/main}'
R = '{http://schemas.openxmlformats.org/package/2006/relationships}'


def inspect_docx(path):
    try:
        archive = ZipFile(path)
    except BadZipFile as exc:
        raise ValueError('not a ZIP-based Word document') from exc
    with archive:
        names = sorted(archive.namelist())
        if 'word/document.xml' not in names:
            raise ValueError('missing word/document.xml')
        result = {'paragraphs': 0, 'tables': 0, 'revision_marks': 0,
                  'field_instructions': 0, 'external_relationships': [],
                  'media_parts': [n for n in names if n.startswith('word/media/') and not n.endswith('/')]}
        for name in names:
            if not (name.startswith('word/') and name.endswith('.xml') or name.endswith('.rels')):
                continue
            try:
                root = ET.fromstring(archive.read(name))
            except ET.ParseError as exc:
                raise ValueError(f'malformed XML: {name}') from exc
            if name == 'word/document.xml':
                result['paragraphs'] = sum(1 for _ in root.iter(W + 'p'))
                result['tables'] = sum(1 for _ in root.iter(W + 'tbl'))
            if name.startswith('word/') and name.endswith('.xml'):
                result['revision_marks'] += sum(1 for e in root.iter() if e.tag in (W + 'ins', W + 'del'))
                result['field_instructions'] += sum(1 for e in root.iter() if e.tag in (W + 'instrText', W + 'fldSimple'))
            if name.endswith('.rels'):
                for rel in root.iter(R + 'Relationship'):
                    if rel.get('TargetMode') == 'External':
                        result['external_relationships'].append({'part': name, 'target': rel.get('Target', '')})
        result['external_relationships'].sort(key=lambda item: (item['part'], item['target']))
        return result


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('path')
    print(json.dumps(inspect_docx(parser.parse_args().path), indent=2))

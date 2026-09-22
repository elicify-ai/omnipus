#!/usr/bin/env node
// typography/css-scan.mjs
//
// The PostCSS-driven CSS scan (font-size/font-family declarations and
// shorthand). Depends only on class-value-rules.mjs, same
// acyclic-by-construction property as module-and-purity.mjs.

'use strict'

import postcss from 'postcss'
import {
  STYLE_ROLE_PROPERTIES,
  FLOOR_PX,
  ROOT_MIN_PX,
  ROOT_MAX_PX,
  VAR_REFERENCE_PATTERN,
  evaluateFontSizeValue,
  evaluateFontFamilyValue,
  parseFontShorthand,
  collapseWhitespace,
  classifyClassToken,
} from './class-value-rules.mjs'

// ---------------------------------------------------------------------------
// CSS scan (PostCSS).
// ---------------------------------------------------------------------------

export function scanCss({ path, source, registeredTokens, resolvedTokenValues = new Map() }) {
  let root
  try {
    root = postcss.parse(source, { from: path })
  } catch (error) {
    return [{
      ruleId: 'typography/parse-failure',
      path,
      syntax: 'CSS parse failure',
      message: `PostCSS could not parse ${path}: ${error.message}. The lock fails closed on unparseable governed files.`,
    }]
  }

  const findings = []
  root.walkDecls((declaration) => {
    // Custom-property assignments are token-graph declarations (D3 lane),
    // not typography usages.
    if (declaration.prop.startsWith('--')) {
      if (declaration.prop.startsWith('--type-') && registeredTokens.has(declaration.prop)) {
        const verdict = evaluateFontSizeValue(declaration.value, registeredTokens, resolvedTokenValues)
        if (verdict.status !== 'ok') findings.push({
          ruleId: 'typography/token-shadow', path, syntax: `${declaration.prop}: ${verdict.printed}`,
          message: `${declaration.prop} shadows a registered typography token with an unsafe value.`,
          line: declaration.source.start.line, column: declaration.source.start.column,
        })
      }
      return
    }
    const parent = declaration.parent
    if (parent && parent.type === 'atrule' && parent.name === 'font-face') return
    const at = { line: declaration.source.start.line, column: declaration.source.start.column }
    const prop = declaration.prop.toLowerCase()
    if (prop === 'font-size') {
      emitCssVerdict(evaluateFontSizeValue(declaration.value, registeredTokens, resolvedTokenValues), 'font-size', at, path, findings, 'size')
      return
    }
    if (prop === 'font-family') {
      emitCssVerdict(evaluateFontFamilyValue(declaration.value, registeredTokens), 'font-family', at, path, findings, 'family')
      return
    }
    if (prop === 'font') {
      emitCssShorthand(declaration.value, at, path, findings, registeredTokens)
      return
    }
    const role = STYLE_ROLE_PROPERTIES.get(prop)
    if (role) emitCssRole(declaration.value, prop, role, at, path, findings, registeredTokens)
  })
  root.walkAtRules('apply', (rule) => {
    const at = { line: rule.source.start.line, column: rule.source.start.column }
    const sink = { registeredTokens, isRegistered: (name) => registeredTokens.has(name), emit: (finding) => findings.push({ path, ...finding, ...at }) }
    for (const token of rule.params.split(/\s+/).filter(Boolean)) classifyClassToken(token, sink)
  })
  return findings
}

export function emitCssRole(value, label, contract, at, path, findings, registeredTokens) {
  const printed = collapseWhitespace(value)
  if (['inherit', 'initial', 'unset', 'revert', 'revert-layer', 'normal'].includes(printed)) return
  const variable = VAR_REFERENCE_PATTERN.exec(printed)
  if (variable && registeredTokens.has(variable[1])) return
  findings.push({
    ruleId: variable ? 'typography/unregistered-typography-token' : contract.ruleId,
    path,
    syntax: `${label}: ${printed}`,
    message: variable ? `${label} references an unregistered typography token.` : `${label} is an arbitrary D9 typography value; use a registered role token.`,
    ...at,
  })
}

export function sizeVerdictFields(status, label, printed) {
  if (status === 'below-floor') {
    return {
      ruleId: 'typography/font-size-below-floor',
      message: `${label}: ${printed} can compute below the ${FLOOR_PX}px floor under the §D1 root range (${ROOT_MIN_PX}–${ROOT_MAX_PX}px); §D2 forbids any UI text below ${FLOOR_PX}px.`,
    }
  }
  if (status === 'unregistered') {
    return {
      ruleId: 'typography/unregistered-font-size-token',
      message: `${label}: ${printed} references a custom property that is not a registered typography token (§D9).`,
    }
  }
  if (status === 'runtime-preference-boundary') {
    return {
      ruleId: 'typography/extension-boundary',
      message: `${label}: ${printed} resolves the runtime user font-size preference (--user-font-size, §D1) against otherwise fully registered floor/ceiling tokens; this is a centrally-reviewed extension boundary, not an unregistered token, and stays blocking until exact central review.`,
    }
  }
  return {
    ruleId: 'typography/unsupported-font-size',
    message: `${label}: ${printed} cannot be bounded against the ${FLOOR_PX}px floor statically (relative or dynamic units); §D2 needs a registered token, a provable expression, or browser-harness proof.`,
  }
}

export function familyVerdictFields(status, label, printed) {
  if (status === 'unregistered') {
    return {
      ruleId: 'typography/unregistered-font-size-token',
      message: `${label}: ${printed} references an unregistered custom property (§D9 family token boundary).`,
    }
  }
  return {
    ruleId: 'typography/font-family-literal',
    message: `${label}: ${printed} is a literal family stack; §D9 requires the Outfit/Inter/JetBrains Mono tokens.`,
  }
}

export function emitCssVerdict({ status, printed }, label, at, path, findings, kind) {
  if (status === 'ok') return
  const fields = kind === 'size' ? sizeVerdictFields(status, label, printed) : familyVerdictFields(status, label, printed)
  findings.push({
    ...fields,
    path,
    syntax: `${label}: ${printed}`,
    line: at.line,
    column: at.column,
  })
}

export function emitCssShorthand(value, at, path, findings, registeredTokens) {
  const parsed = parseFontShorthand(value)
  if (parsed.systemKeyword || parsed.unsupported) {
    findings.push({
      ruleId: 'typography/unsupported-font-size',
      path,
      syntax: `font: ${collapseWhitespace(value)}`,
      message: parsed.systemKeyword
        ? 'System font keywords carry an implementation-defined size the static lock cannot bound against the 12px floor.'
        : 'This font shorthand could not be split into size and family by the static lock.',
      line: at.line,
      column: at.column,
    })
    return
  }
  if (parsed.size) {
    const verdict = evaluateFontSizeValue(parsed.size, registeredTokens)
    if (verdict.status !== 'ok') {
      emitCssVerdict({ ...verdict, printed: `${verdict.printed} …` }, 'font', at, path, findings, 'size')
    }
  }
  if (parsed.family && parsed.family.trim().length > 0) {
    const verdict = evaluateFontFamilyValue(parsed.family, registeredTokens)
    if (verdict.status !== 'ok') {
      emitCssVerdict({ ...verdict, printed: `… ${verdict.printed}` }, 'font', at, path, findings, 'family')
    }
  }
}

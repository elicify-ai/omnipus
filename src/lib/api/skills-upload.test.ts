import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { installSkillFromFile } from './skills'
import { ApiSchemaError } from './http'

const revision = 'a'.repeat(64)
const activeSkill = {
  revision,
  persistence_status: 'complete' as const,
  activation_status: 'active' as const,
  changed_fields: ['skill'],
  id: 'local-skill',
  name: 'Local skill',
  version: '1.0.0',
  verified: false,
  status: 'active',
}

const fetchMock = vi.fn()

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

beforeEach(() => {
  fetchMock.mockReset()
  vi.stubGlobal('fetch', fetchMock)
  vi.spyOn(document, 'cookie', 'get').mockReturnValue('__Host-csrf=test-csrf')
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('installSkillFromFile', () => {
  it('uploads Markdown and installs with the returned opaque ref', async () => {
    fetchMock
      .mockResolvedValueOnce(jsonResponse({
        files: [{
          name: 'local-skill.md',
          path: 'uploads/upload-context/local-skill.md',
          size: 18,
          content_type: 'text/markdown',
          ref: 'media://opaque-upload-ref',
        }],
      }, 201))
      .mockResolvedValueOnce(jsonResponse(activeSkill))
    const file = new File(['# Local skill\nBody'], 'local-skill.md', { type: 'text/markdown' })

    await expect(installSkillFromFile(file, 'upload-context')).resolves.toEqual(activeSkill)

    expect(fetchMock).toHaveBeenCalledTimes(2)
    const [uploadURL, uploadInit] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(uploadURL).toBe('/api/v1/upload')
    expect(uploadInit).toMatchObject({
      method: 'POST',
      credentials: 'include',
      headers: { 'X-CSRF-Token': 'test-csrf' },
    })
    const form = uploadInit.body as FormData
    expect(form.get('session_id')).toBe('upload-context')
    expect(form.get('files')).toBe(file)

    const [installURL, installInit] = fetchMock.mock.calls[1] as [string, RequestInit]
    expect(installURL).toBe('/api/v1/skills/install')
    expect(JSON.parse(String(installInit.body))).toEqual({ upload_id: 'media://opaque-upload-ref' })
  })

  it('preserves ZIP bytes and includes the reviewed installed revision', async () => {
    const zipBytes = new Uint8Array([0x50, 0x4b, 0x03, 0x04, 0x00, 0xff, 0x7f])
    const file = new File([zipBytes], 'package.zip', { type: 'application/zip' })
    fetchMock
      .mockImplementationOnce(async (_url: string, init: RequestInit) => {
        const uploaded = (init.body as FormData).get('files') as File
        expect(new Uint8Array(await uploaded.arrayBuffer())).toEqual(zipBytes)
        return jsonResponse({
          files: [{
            name: 'package.zip', path: 'uploads/upload-context/package.zip',
            size: zipBytes.byteLength, content_type: 'application/zip', ref: 'media://zip-ref',
          }],
        }, 201)
      })
      .mockResolvedValueOnce(jsonResponse({ ...activeSkill, id: 'package', revision }))

    await installSkillFromFile(file, 'upload-context', revision)

    const [, installInit] = fetchMock.mock.calls[1] as [string, RequestInit]
    expect(JSON.parse(String(installInit.body))).toEqual({
      upload_id: 'media://zip-ref',
      revision,
    })
  })

  it('rejects an upload without an opaque ref and never substitutes its path', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({
      files: [{
        name: 'local-skill.md', path: 'uploads/upload-context/local-skill.md',
        size: 18, content_type: 'text/markdown',
      }],
    }, 201))
    const file = new File(['# Local skill\nBody'], 'local-skill.md', { type: 'text/markdown' })

    await expect(installSkillFromFile(file, 'upload-context')).rejects.toThrow(
      'Upload completed without an installable media reference.',
    )
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it.each([
    { ...activeSkill, persistence_status: 'partial', activation_status: 'not_attempted' },
    { ...activeSkill, activation_status: 'failed' },
  ])('rejects incomplete install state %#', async (state) => {
    fetchMock
      .mockResolvedValueOnce(jsonResponse({
        files: [{
          name: 'local-skill.md', path: 'uploads/upload-context/local-skill.md',
          size: 18, content_type: 'text/markdown', ref: 'media://opaque-upload-ref',
        }],
      }, 201))
      .mockResolvedValueOnce(jsonResponse(state))

    await expect(installSkillFromFile(
      new File(['# Local skill\nBody'], 'local-skill.md'),
      'upload-context',
    )).rejects.toMatchObject({
      name: 'ConfigurationSaveError',
      state: {
        revision: state.revision,
        persistence_status: state.persistence_status,
        activation_status: state.activation_status,
        changed_fields: state.changed_fields,
      },
    })
  })

  it('rejects a malformed successful install envelope', async () => {
    fetchMock
      .mockResolvedValueOnce(jsonResponse({
        files: [{
          name: 'local-skill.md', path: 'uploads/upload-context/local-skill.md',
          size: 18, content_type: 'text/markdown', ref: 'media://opaque-upload-ref',
        }],
      }, 201))
      .mockResolvedValueOnce(jsonResponse({ ...activeSkill, changed_fields: null }))

    await expect(installSkillFromFile(
      new File(['# Local skill\nBody'], 'local-skill.md'),
      'upload-context',
    )).rejects.toBeInstanceOf(ApiSchemaError)
  })
})

import type { Meta, StoryObj } from '@storybook/react-vite'
import { BashOutputBlock } from './BashOutput'
import { FileReadBlock } from './FileReadPreview'
import { FileTreeBlock } from './FileTreeView'
import { WebSearchBlock } from './WebSearchResult'
import { WebFetchBlock } from './WebFetchPreview'
import { JudgeVerdictThreadCard } from '../JudgeVerdictThreadCard'

/**
 * ToolUIBlocks — the chat thread's dedicated tool-call rows
 * (toolui-analysis items 1-5, founder-approved 2026-09-26).
 *
 * Every dedicated row starts COLLAPSED: the one-line header carries the tool
 * label, a ~60-char command/path/query summary, and the status; the full
 * command, output, file content, tree, results, or criteria live inside the
 * DisclosureRow accordion body.
 *
 * Pure renders ON PURPOSE — no play() here. Storybook's static build
 * AUTO-RUNS play() on story load, which races any external browser check:
 * play() leaves the story in whatever state it last touched and logs its own
 * assertion errors into the page console before an external script has read a
 * single attribute (observed 2026-09-26: every story failed the external
 * check with play()-origin console errors). The collapsed-by-default and
 * expand/collapse contracts are asserted where they belong — BashOutput.block.test.tsx,
 * ChatScreen.tool-replay-parity.test.tsx and JudgeVerdictThreadCard.test.tsx
 * (vitest), plus the external browser check against this static build.
 */

const meta = {
  title: 'Chat/Tool UI blocks',
  parameters: { layout: 'padded' },
} satisfies Meta
export default meta
type Story = StoryObj

export const BashBlock: Story = {
  name: 'bash - collapsed by default, expands to full command + output',
  render: () => (
    <BashOutputBlock
      toolName="bash"
      args={{ command: 'deploy/verify-all.sh --target prod --verbose' }}
      result={'verify ok\n312 checks passing\nartifacts signed\n'}
      isRunning={false}
    />
  ),
}

export const BashBlockError: Story = {
  name: 'bash - error header while collapsed',
  render: () => (
    <BashOutputBlock
      toolName="bash"
      args={{ command: 'deploy/verify-all.sh --target staging' }}
      result="verify failed - 2 checks red"
      isRunning={false}
      isError
    />
  ),
}

export const BashBlockRunning: Story = {
  name: 'bash - running, expand shows the Executing spinner',
  render: () => (
    <BashOutputBlock toolName="bash" args={{ command: 'tail -f app.log' }} result={null} isRunning />
  ),
}

export const FileReadStory: Story = {
  name: 'read_file - collapsed, expands to file content',
  render: () => (
    <FileReadBlock
      toolName="read_file"
      args={{ path: '/ws/pkg/tools/web.go' }}
      result={'package tools\n\nconst ToolName = "search_web"\n'}
      isRunning={false}
    />
  ),
}

export const FileTreeStory: Story = {
  name: 'list_directory - collapsed, expands to the tree panel',
  render: () => (
    <FileTreeBlock
      toolName="list_directory"
      args={{ path: '/ws/pkg/tools' }}
      result={'web.go\nfilesystem.go\nregistry.go\n'}
      isRunning={false}
    />
  ),
}

export const WebSearchStory: Story = {
  name: 'search_web - collapsed, expands to the parsed result list',
  render: () => (
    <WebSearchBlock
      toolName="search_web"
      args={{ query: 'omnipus single binary' }}
      result={'1. Omnipus\n   https://omnipus.ai\n   agentic core'}
      isRunning={false}
    />
  ),
}

export const WebFetchStory: Story = {
  name: 'fetch_url - collapsed, expands to the fetched content',
  render: () => (
    <WebFetchBlock
      toolName="fetch_url"
      args={{ url: 'https://omnipus.ai/docs' }}
      result={'<html>omnipus docs</html>'}
      isRunning={false}
    />
  ),
}

export const JudgeVerdictStory: Story = {
  name: 'judge verdict - collapsed one-liner, criteria inside the accordion',
  render: () => (
    <JudgeVerdictThreadCard
      verdict={{
        id: 'verdict-story-1',
        scope: 'task',
        task_id: 'task-1',
        round: 2,
        met: false,
        per_criterion: [
          { criterion_id: 'tests-pass', met: false, reason: '2 checks still red.' },
          { criterion_id: 'lint-clean', met: true, reason: 'gofmt + vet clean.' },
        ],
        model: 'z-ai/glm-5-turbo',
        judged_at: '2026-09-26T12:00:00Z',
        judge_agent_id: 'judge',
      }}
    />
  ),
}

import { describe, expect, it } from 'vitest'
import type { AgentUIMessage } from '@/lib/agent-ui'
import { agentViewToRenderMessage, buildAgentMessageViews } from './agent-message-view'

describe('agentViewToRenderMessage', () => {
  const chapter = 'Chapter body. '.repeat(500)
  const history: AgentUIMessage[] = [{
    id: 'write-reply', role: 'assistant', metadata: { run_id: 'run-a' },
    parts: [{
      type: 'tool-write', toolCallId: 'call-a', state: 'output-available',
      input: { path: 'chapters/001.md', content: chapter },
      output: JSON.stringify({ status: 'success', path: 'chapters/001.md' }),
    } as AgentUIMessage['parts'][number]],
  }]

  it('reuses the converted message when a settled row remounts', () => {
    const [view] = buildAgentMessageViews(history)
    const first = agentViewToRenderMessage(view)

    expect(first).toMatchObject({ role: 'tool_call', name: 'write', args: expect.stringContaining('chapters/001.md') })
    expect(agentViewToRenderMessage(buildAgentMessageViews(history)[0])).toBe(first)
  })

  it('builds a separate settled copy for forced completion', () => {
    const [view] = buildAgentMessageViews([{ ...history[0], id: 'streaming-reply', parts: [{ type: 'text', text: 'Drafting', state: 'streaming' }] }])

    expect(agentViewToRenderMessage(view)).toMatchObject({ role: 'assistant', streaming: true })
    expect(agentViewToRenderMessage(view, { forceDone: true })).toMatchObject({ role: 'assistant', streaming: false })
    expect(agentViewToRenderMessage(view)).toMatchObject({ streaming: true })
  })
})

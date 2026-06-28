/**
 * SessionBar — stub.
 *
 * The SessionBar's functionality (agent picker, New Chat, model selector, token
 * counter, Sessions button) has been consolidated into ChatControls. This stub
 * satisfies the TopBar import until TopBar is updated by its owning agent to
 * render ChatControls instead.
 *
 * formatTokens is re-exported so existing test imports continue to work.
 */
export { ChatControls as SessionBar } from './ChatControls'
export { formatTokens } from '@/lib/formatTokens'

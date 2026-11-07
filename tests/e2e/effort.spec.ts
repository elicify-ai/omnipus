/** T-E1, spec r10 S-07/S-08/S-13/S-21/S-41/S-95.
 * Real SPA, isolated authenticated gateway, process-edge provider/catalog and
 * recorded-history peers only. Collection is NOT RED proof; execute this file.
 * This is deterministic browser projection coverage; the Go production-turn
 * suite separately proves metadata writers, accounting and history transports.
 */
import { test as base, expect, type Page, type Locator } from '@playwright/test'
import http from 'node:http'
import { GatewayProcess } from './fixtures/gateway-process'
import { historyDevOrigin } from './fixtures/chat-history-paging/dev-origin'
import { createTerminalSession } from './fixtures/terminal-outcome-acceptance'
import { openSessionByDeepLink } from './fixtures/session-setup'
import { stubOnboarding } from './fixtures/onboarding-stubs'
import { wireHistoryPage, wireHistorySnapshot, wireAvailableDetail, fixtureHistoryPosition } from '../chat-history-paging/legacy-wire'
import type { ClientFrame, ServerFrame } from '../../src/lib/api/generated/asyncapi-types'
import { WsFrame } from '../../src/lib/api/generated/ws-schemas'

// A fixture model id (never a real one: scripts/check-no-hardcoded-e2e-model.sh); the
// stub peer and the routed catalog below are the only places that serve it.
const MODEL='effort-fixture/levels-model'
const NOTE=`Sent as Low: ${MODEL} doesn't offer Medium.`
const CATALOG={schema_version:'2.0.0',version:'v2026.10.31',updated_at:'2026-10-31T00:00:00Z',source:'test',served_from:'embedded',stale:false,
 default_resize_limits:{long_edge_px:2048,max_bytes:10485760},providers:[{id:'openrouter',name:'OpenRouter',company:'OpenRouter',api:'https://openrouter.ai/api/v1',protocol:'openai-compatible',protocols:[{protocol:'openai-compatible',api:'https://openrouter.ai/api/v1'}],tier:'popular',auth_methods:['api_key'],aliases:[],locality:'cloud',models:[{
 id:MODEL,name:MODEL,context_window:128000,max_output_tokens:8192,input_modalities:['text'],tool_call:true,status:'active',reasoning:{levels:['none','low','high','max'],switch:false}}]}]}

type Environment={origin:string;sessionId:string;otherId:string;agentId:string;gateway:GatewayProcess}
const test=base.extend<{effort:Environment}>({
 effort:async({page},use,info)=>{
  const peer=http.createServer((req,res)=>{const chunks:Buffer[]=[];req.on('data',c=>chunks.push(c));req.on('end',()=>{
   res.setHeader('Content-Type','application/json')
   if(req.method==='GET'){res.end(JSON.stringify({object:'list',data:[{id:MODEL}]}));return}
   const body=JSON.parse(Buffer.concat(chunks).toString()||'{}')
   if(body.stream){res.setHeader('Content-Type','text/event-stream');res.end('data: '+JSON.stringify({choices:[{index:0,delta:{content:'EFFORT-ANSWER-7731'},finish_reason:'stop'}]})+'\n\ndata: [DONE]\n\n')}
   else res.end(JSON.stringify({choices:[{message:{role:'assistant',content:'probe ok'},finish_reason:'stop'}]}))
  })})
  await new Promise<void>(resolve=>peer.listen(0,'127.0.0.1',resolve))
  const address=peer.address();if(!address||typeof address==='string')throw new Error('provider did not bind')
  let gateway:GatewayProcess|undefined;let dev:Awaited<ReturnType<typeof historyDevOrigin>>|undefined
  try {
   gateway=await GatewayProcess.start({apiBase:`http://127.0.0.1:${address.port}`,model:MODEL})
   dev=await historyDevOrigin(gateway.baseURL,info)
   const ids=await createTerminalSession(gateway)
   await page.context().addCookies((await gateway.browserStorageState()).cookies)
   await page.route('**/api/v1/providers/catalog*',route=>route.fulfill({status:200,contentType:'application/json',body:JSON.stringify(CATALOG)}))
   await use({origin:dev.origin,sessionId:ids.session.id,otherId:ids.otherSession.id,agentId:ids.agent.id,gateway})
  } finally {if(dev)await dev.close();if(gateway)await gateway.stop();await new Promise<void>((resolve,reject)=>{peer.close(err=>err?reject(err):resolve());peer.closeAllConnections()})}
 }
})
test.use({storageState:{cookies:[],origins:[]}})
test.describe.configure({retries:0})

async function open(page:Page,e:Environment){await openSessionByDeepLink(page,e.sessionId,e.origin)}
function observeSends(page:Page){const sends:ClientFrame[]=[];page.on('websocket',socket=>{if(!socket.url().includes('/api/v1/chat/ws'))return;socket.on('framesent',({payload})=>{const frame=JSON.parse(payload.toString());if(frame.type==='message')sends.push(frame)})});return sends}

// Source entries, identities, positions and envelope metadata are independently
// constructed test data. The renderer, decoder and paging state stay real.
async function recordedHistory(page:Page,e:Environment,paged:boolean,withEffort=true){
 const timestamp='2026-10-31T09:00:00Z'
 const answer=(id:string,content:string,effort:unknown,count?:number)=>({id,type:'message',role:'assistant',agent_id:e.agentId,model:MODEL,content,timestamp,...(withEffort?{effort,...(count===undefined?{}:{turn_reasoning_tokens:count})}:{}),history_position:fixtureHistoryPosition(e.sessionId,id)})
 const mapped={chosen:'medium',sent:'low',reason:'mapped',provider:'openrouter',model:MODEL}
 const exact={chosen:'high',sent:'high',provider:'openrouter',model:MODEL}
 const answers=paged
  ? [answer('effort-mapped','MAPPED-ANSWER-7731',mapped,120),answer('effort-tool','TOOL-ANSWER-7731',exact,30),answer('effort-unreported','UNREPORTED-ANSWER-7731',{chosen:'provider_default'})]
  : [answer('effort-high','HIGH-ANSWER-7731',exact,120),answer('effort-mapped','MAPPED-ANSWER-7731',mapped,30),answer('effort-unreported','UNREPORTED-ANSWER-7731',{chosen:'provider_default'})]
 const newer=Array.from({length:40},(_,i)=>({id:`newer-${i}`,type:'message',role:'user',agent_id:e.agentId,content:`NEWER-${i}\n${'later paragraph '.repeat(40)}`,timestamp,history_position:fixtureHistoryPosition(e.sessionId,`newer-${i}`)}))
 const initial=wireHistoryPage(e.sessionId,paged?newer:answers,paged?40:3,{earlier:paged})
 const older=wireHistoryPage(e.sessionId,answers,3,{later:true,info:{mode:'older'}})
 let olderFetched=0
 await page.route(`**/api/v1/sessions/${e.sessionId}/messages*`,async route=>{const url=new URL(route.request().url());const before=url.searchParams.get('before');if(before){expect(before).toBe(initial.next_before);olderFetched++}await route.fulfill({status:200,contentType:'application/json',body:JSON.stringify(before?older:initial)})})
 const detail=await e.gateway.apiFetch('GET',`/api/v1/sessions/${e.sessionId}`)
 expect(detail.ok,detail.raw).toBe(true)
 const metadata=(detail.body as {session:unknown}).session
 await page.route(`**/api/v1/sessions/${e.sessionId}`,route=>route.fulfill({status:200,contentType:'application/json',body:JSON.stringify(wireAvailableDetail(metadata,initial))}))
 await page.routeWebSocket('**/api/v1/chat/ws*',socket=>{
  const server=socket.connectToServer();server.onMessage(raw=>socket.send(raw))
  socket.onMessage(raw=>{
   const frame=JSON.parse(raw.toString()) as ClientFrame
   if(frame.type!=='attach_session'||frame.session_id!==e.sessionId){server.send(raw);return}
   const entries=initial.entries as Array<Record<string,unknown>>
   const carriers=entries.map(entry=>({type:'replay_message',session_id:e.sessionId,id:entry.id,role:entry.role,agent_id:e.agentId,content:entry.content,model:entry.model,timestamp,
    ...(entry.effort===undefined?{}:{effort:entry.effort}),...(entry.turn_reasoning_tokens===undefined?{}:{turn_reasoning_tokens:entry.turn_reasoning_tokens}),history_origin:{position:entry.history_position,entry_id:entry.id}}))
   // Validate the pre-feature base shape before trusting a new-field RED.
   for(const carrier of carriers){const {effort,turn_reasoning_tokens,...bare}=carrier;void effort;void turn_reasoning_tokens;expect(WsFrame.safeParse(bare).success).toBe(true)}
   const history={...initial,page_info:{...initial.page_info,limit:frame.history_limit??40,min_rows:frame.history_min_rows??20}}
   const snapshot=wireHistorySnapshot(history,{seq:100,boot_id:'effort-fixture',reason:'unknown_position'},carriers as ServerFrame[])
   for(const reply of [snapshot,...carriers,{type:'done',session_id:e.sessionId,stats:{frames_emitted:carriers.length}},{type:'catch_up_complete',session_id:e.sessionId,seq:100,boot_id:'effort-fixture',mode:'snapshot'}])socket.send(JSON.stringify(reply))
  })
 })
 return {olderFetched:()=>olderFetched,answers}
}

// The spec fixes the tooltip text, not a new button's accessible name. Drive
// the real row's hover/focus targets instead of inventing a "stats" selector.
async function showThinkingTooltip(page:Page,row:Locator,text:string){
 const targets=row.locator('button, [tabindex], [data-testid="message-model"]')
 await expect(targets.first()).toBeVisible()
 for(let i=0;i<await targets.count();i++){
  await targets.nth(i).hover()
  if(await page.getByRole('tooltip').filter({hasText:text}).isVisible())return
 }
 await expect(page.getByRole('tooltip').filter({hasText:text})).toBeVisible()
}

test('S-07/S-08: a served DeepSeek model offers radios with no Medium',async({page,effort})=>{
 await open(page,effort)
 await expect(page.getByTestId('effort-chip')).toBeVisible();await page.getByTestId('effort-chip').click()
 const radios=page.getByRole('radio');await expect(radios).toHaveCount(6)
 expect((await radios.allTextContents()).map(s=>s.trim()).slice(1)).toEqual(['Provider default','Off','Low','High','Max'])
 await expect(page.getByRole('radio',{name:'Medium',exact:true})).toHaveCount(0)
})
test('S-13: the first actual WebSocket message carries the exact pair and High across a pre-send refresh',async({page,effort})=>{
 const sends=observeSends(page);await open(page,effort)
 await page.getByTestId('composer-model-selector').click();await page.getByText(MODEL,{exact:true}).last().click()
 await expect(page.getByTestId('effort-chip')).toBeVisible();await page.getByTestId('effort-chip').click();await page.getByRole('radio',{name:'High',exact:true}).click()
 await page.reload();await expect(page.getByTestId('chat-input')).toBeEnabled()
 await expect(page.getByTestId('effort-chip')).toBeVisible();await page.getByTestId('effort-chip').click();await expect(page.getByRole('radio',{name:'High',exact:true})).toHaveAttribute('aria-checked','true');await page.keyboard.press('Escape')
 await page.getByTestId('chat-input').fill('first effort send 7731');await page.getByTestId('chat-input').press('Enter')
 await expect.poll(()=>sends.length).toBe(1)
 expect(sends[0]).toMatchObject({type:'message',content:'first effort send 7731',agent_id:effort.agentId,metadata:{provider:'openrouter',model_name:MODEL,reasoning_effort:'high'}})
})
test('S-21: saved answers keep exact mapped note, no note for exact High, and reported figures after reload',async({page,effort})=>{
 await recordedHistory(page,effort,false);await open(page,effort)
 const check=async()=>{
  await expect(page.getByText('HIGH-ANSWER-7731',{exact:true})).toBeVisible()
  const high=page.locator('[data-message-id="effort-high"]')
  await expect(high.getByText(/^(Sent as |Effort not sent:)/)).toHaveCount(0)
  await showThinkingTooltip(page,high,'Thinking: 120 tokens (reported by the provider)')
  await expect(page.getByRole('tooltip')).toContainText('Effort: High')
  await expect(page.getByRole('tooltip')).not.toContainText(/Output:|output tokens/i)
  await expect(page.getByText(NOTE,{exact:true})).toBeVisible()
  await showThinkingTooltip(page,page.locator('[data-message-id="effort-unreported"]'),'Thinking: not reported')
  await expect(page.getByText(/answered at/i)).toHaveCount(0)
  await expect(page.getByText('This saved message could not be displayed.',{exact:true})).toHaveCount(0)
 }
 await check();await page.reload();await check()
})
test('S-41: web /effort max stays local, sends no server message, and resets in another conversation',async({page,effort})=>{
 const sends=observeSends(page);let writes=0;await page.route('**/api/v1/agents/**',async route=>{if(route.request().method()!=='GET')writes++;await route.continue()})
 await open(page,effort);await page.getByTestId('chat-input').fill('/effort max');await page.getByTestId('chat-input').press('Enter')
 await expect(page.getByTestId('effort-chip')).toContainText('Max, set for this conversation');expect(sends).toEqual([]);expect(writes).toBe(0)
 await openSessionByDeepLink(page,effort.otherId,effort.origin);await expect(page.getByTestId('effort-chip')).not.toContainText('Max, set for this conversation')
})
test('S-95: scrolling real history viewport and reloading preserves all three recorded-answer notes and counts',async({page,effort})=>{
 const history=await recordedHistory(page,effort,true);await open(page,effort)
 const check=async(expectedFetches:number)=>{
  await expect(page.getByText('NEWER-39',{exact:false})).toBeVisible()
  await page.getByTestId('virtualized-message-list').evaluate(el=>{el.scrollTop=0;el.dispatchEvent(new Event('scroll'))})
  await expect.poll(history.olderFetched).toBe(expectedFetches)
  for(const [id,text,thinking] of [
   ['effort-mapped','MAPPED-ANSWER-7731','Thinking: 120 tokens (reported by the provider)'],
   ['effort-tool','TOOL-ANSWER-7731','Thinking: 30 tokens (reported by the provider)'],
   ['effort-unreported','UNREPORTED-ANSWER-7731','Thinking: not reported'],
  ]){
   const row=page.locator(`[data-message-id="${id}"]`);await expect(row).toContainText(text)
   if(id==='effort-mapped')await expect(row).toContainText(NOTE)
   else await expect(row.getByText(/^(Sent as |Effort not sent:)/)).toHaveCount(0)
   await showThinkingTooltip(page,row,thinking)
  }
  await expect(page.getByText('This saved message could not be displayed.',{exact:true})).toHaveCount(0)
 }
 await check(1);await page.reload();await check(2)
})

test('PIN: the same recorded-history envelope renders a baseline answer without new effort fields',async({page,effort})=>{
 await recordedHistory(page,effort,false,false);await open(page,effort)
 await expect(page.getByText('HIGH-ANSWER-7731',{exact:true})).toBeVisible()
 await expect(page.getByText('This saved message could not be displayed.',{exact:true})).toHaveCount(0)
})

test('S-61 wizard: Change now puts High on the declared completion variant with no follow-up default write',async({page,effort})=>{
 const wire=await stubOnboarding(page)
 await page.route('**/api/v1/providers/catalog*',route=>route.fulfill({status:200,contentType:'application/json',body:JSON.stringify(CATALOG)}))
 let defaultWrites=0
 await page.route('**/api/v1/providers/default-model',async route=>{if(route.request().method()!=='GET')defaultWrites++;await route.continue()})
 await page.goto(`${effort.origin}/#/onboarding`)
 await expect(page.getByText('Step 1 of 4').first()).toBeVisible()
 await page.locator('#admin-username').fill('effort-test-admin')
 await page.getByRole('button',{name:/^continue$/i}).click()
 await page.locator('#admin-password').fill('test-password-7731')
 await page.locator('#admin-password-confirm').fill('test-password-7731')
 await page.getByRole('button',{name:/^continue$/i}).click()
 await page.locator('#pref-name').fill('Effort test')
 await page.getByRole('button',{name:/^continue$/i}).click()
 await expect(page.getByText('Step 4 of 4').first()).toBeVisible()
 await page.getByTestId('picker-popular-openrouter').click()
 await page.getByTestId('provider-detail-panel-api-key-input').fill('test-placeholder-not-a-secret')
 await page.getByTestId('provider-detail-panel-continue').click()
 await page.getByTestId('onboarding-model-select').click()
 await page.getByTestId(`onboarding-model-${MODEL}`).click()
 await expect(page.getByText('Change now',{exact:true})).toBeVisible()
 await page.getByText('Change now',{exact:true}).click()
 await page.getByRole('radio',{name:'High',exact:true}).click()
 await page.getByRole('button',{name:/finish/i}).click()
 await expect.poll(()=>wire.completions.length).toBe(1)
 expect(wire.completions[0].provider).toMatchObject({auth_method:'api_key',id:'openrouter',model:MODEL,reasoning_effort:'high'})
 expect(wire.completions[0]).not.toHaveProperty('reasoning_effort')
 expect(defaultWrites).toBe(0)
})

// S-86 chat caller: the actual composer owns the pending switch. API edges
// provide stored inherited Off; no mapper/control/transaction is substituted.
async function inheritedChat(page:Page,e:Environment,target:string){
 const toLevels='openai/gpt-6.1-sol';const toMuted='budget-only-model'
 const agents=await e.gateway.apiFetch('GET','/api/v1/agents');expect(agents.ok,agents.raw).toBe(true)
 const rows=agents.body as Array<Record<string,unknown>>
 expect(rows.some(row=>row.id===e.agentId)).toBe(true)
 const given=rows.map(row=>row.id===e.agentId?{...row,provider:'openrouter',model:MODEL,reasoning_effort:'off'}:row)
 await page.route('**/api/v1/agents',route=>route.fulfill({status:200,contentType:'application/json',body:JSON.stringify(given)}))
 await page.route(`**/api/v1/agents/${e.agentId}`,route=>route.fulfill({status:200,contentType:'application/json',body:JSON.stringify(given.find(row=>row.id===e.agentId))}))
 const catalog=structuredClone(CATALOG);const original=catalog.providers[0].models[0]
 catalog.providers[0].models.push({...original,id:toLevels,name:toLevels,reasoning:{levels:['low','medium','high'],switch:false}},{...original,id:toMuted,name:toMuted,reasoning:{levels:[],switch:false}})
 await page.route('**/api/v1/providers/catalog*',route=>route.fulfill({status:200,contentType:'application/json',body:JSON.stringify(catalog)}))
 const providers=await e.gateway.apiFetch('GET','/api/v1/providers');expect(providers.ok,providers.raw).toBe(true)
 const connected=providers.body as Array<Record<string,unknown>>;expect(connected.length).toBeGreaterThan(0)
 await page.route('**/api/v1/providers',route=>route.fulfill({status:200,contentType:'application/json',body:JSON.stringify([{...connected[0],id:'openrouter',name:'openrouter',display_name:'OpenRouter',status:'connected',models:[MODEL,toLevels,toMuted]}])}))
 await open(page,e)
 await expect(page.getByTestId('effort-chip')).toContainText("Agent's setting: Off")
 await page.getByTestId('composer-model-selector').click();await page.getByText(target,{exact:true}).last().click()
 return page.getByTestId('composer-model-selector')
}

test('S-86 chat: inherited Off blocks a no-Off model until High is explicitly chosen',async({page,effort})=>{
 const sends=observeSends(page);await inheritedChat(page,effort,'openai/gpt-6.1-sol')
 await expect(page.getByRole('radio',{name:'Low',exact:true})).toBeFocused()
 await expect(page.getByRole('radio',{name:'Low',exact:true})).toHaveAttribute('aria-checked','false')
 expect(sends).toEqual([])
 await page.getByRole('radio',{name:'High',exact:true}).click()
 await expect(page.getByTestId('effort-chip')).toContainText('High')
 await page.getByTestId('chat-input').fill('inherited Off explicitly replaced 7731');await page.getByTestId('chat-input').press('Enter')
 await expect.poll(()=>sends.length).toBe(1)
 expect(sends[0]).toMatchObject({metadata:{provider:'openrouter',model_name:'openai/gpt-6.1-sol',reasoning_effort:'high'}})
})
test('S-86 chat: Escape preserves the previous model and Not set when effective inherited Off blocks',async({page,effort})=>{
 const sends=observeSends(page);const trigger=await inheritedChat(page,effort,'openai/gpt-6.1-sol')
 await page.keyboard.press('Escape');await expect(trigger).toContainText(MODEL);await expect(trigger).toBeFocused()
 await expect(page.getByTestId('effort-chip')).toContainText("Agent's setting: Off");expect(sends).toEqual([])
 await page.getByTestId('effort-chip').click();await expect(page.getByRole('radio',{name:/^(Not set|Agent's setting)/i})).toHaveAttribute('aria-checked','true')
})
test('S-86 chat: inherited Off to nothing sendable stores Provider default only after the explicit escape choice',async({page,effort})=>{
 const sends=observeSends(page);await inheritedChat(page,effort,'budget-only-model')
 await page.getByRole('button',{name:'Let the provider decide',exact:true}).click()
 await page.getByTestId('chat-input').fill('provider decides 7731');await page.getByTestId('chat-input').press('Enter')
 await expect.poll(()=>sends.length).toBe(1)
 expect(sends[0]).toMatchObject({metadata:{provider:'openrouter',model_name:'budget-only-model',reasoning_effort:'provider_default'}})
})
for(const action of ['Cancel','Escape'])test(`S-86 chat: ${action} on nothing sendable keeps previous model and Not set`,async({page,effort})=>{
 const sends=observeSends(page);const trigger=await inheritedChat(page,effort,'budget-only-model')
 if(action==='Escape')await page.keyboard.press('Escape');else await page.getByRole('button',{name:'Cancel',exact:true}).click()
 await expect(trigger).toContainText(MODEL);await expect(page.getByTestId('effort-chip')).toContainText("Agent's setting: Off");expect(sends).toEqual([])
 await page.getByTestId('effort-chip').click();await expect(page.getByRole('radio',{name:/^(Not set|Agent's setting)/i})).toHaveAttribute('aria-checked','true')
})

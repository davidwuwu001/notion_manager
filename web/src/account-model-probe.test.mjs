import { test } from 'node:test'
import assert from 'node:assert/strict'
import { testAccountModel } from './api.ts'

test('model test pins requested identity and uses management session, never sends a key', async () => {
  const oldFetch = globalThis.fetch
  globalThis.fetch = async (url, request) => {
    assert.equal(url, '/admin/accounts/test')
    assert.equal(request.credentials, 'same-origin')
    assert.equal(request.headers.Authorization, undefined)
    assert.deepEqual(JSON.parse(request.body), { account_id: 'fixture-id', model: 'fixture-model' })
    return new Response(JSON.stringify({ok:true,duration_ms:123}), {status:200})
  }
  try { assert.deepEqual(await testAccountModel('fixture-id','fixture-model'), {ok:true,duration_ms:123}) }
  finally { globalThis.fetch = oldFetch }
})

test('test errors do not expose unknown response content and expire management session', async () => {
  const oldFetch=globalThis.fetch, oldWindow=globalThis.window, events=[]
  globalThis.window={dispatchEvent: event => events.push(event.type)}
  try {
    globalThis.fetch=async()=>new Response(JSON.stringify({ok:false,reason:'secret-token'}),{status:200})
    assert.equal((await testAccountModel('id','model')).reason,'inference_failed')
    globalThis.fetch=async()=>new Response('{}',{status:429})
    assert.equal((await testAccountModel('id','model')).reason,'test_busy')
    globalThis.fetch=async()=>new Response('{}',{status:401})
    await testAccountModel('id','model')
    assert.deepEqual(events,['notion-manager-unauthorized'])
  } finally { globalThis.fetch=oldFetch; globalThis.window=oldWindow }
})

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'

test('dashboard translations have matching keys and all literal upgrade/quota labels exist', () => {
  const zh = JSON.parse(readFileSync(new URL('./locales/zh.json', import.meta.url), 'utf8'))
  const en = JSON.parse(readFileSync(new URL('./locales/en.json', import.meta.url), 'utf8'))
  assert.deepEqual(Object.keys(zh.upgrade).sort(), Object.keys(en.upgrade).sort())
  assert.deepEqual(Object.keys(zh.quota).sort(), Object.keys(en.quota).sort())
  const files = ['App.tsx', ...readdirSync(new URL('./components/', import.meta.url)).filter(file => file.endsWith('.tsx')).map(file => 'components/' + file)]
  for (const file of files) {
    const source = readFileSync(new URL(file, import.meta.url), 'utf8')
    for (const match of source.matchAll(/['"](upgrade|quota)\.([a-z_]+)['"]/g)) {
      assert.equal(typeof zh[match[1]][match[2]], 'string', `${file}: missing zh ${match[1]}.${match[2]}`)
      assert.equal(typeof en[match[1]][match[2]], 'string', `${file}: missing en ${match[1]}.${match[2]}`)
    }
  }
})

import { chromium, expect } from '@playwright/test';
import { mkdir, writeFile } from 'node:fs/promises';

const out = '.omo/evidence/mr-qa-browser';
await mkdir(out, { recursive: true });
const browser = await chromium.launch({ executablePath: '/Users/shicheng_lei/.agent-browser/browsers/chrome-148.0.7778.97/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing' });
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const results = [];
const errors = [];
page.on('pageerror', error => errors.push(error.message));
const button = name => page.getByRole('button', { name, exact: true });
const shot = name => page.screenshot({ path: `${out}/${name}.png` });
async function check(name, run) {
  try { await run(); results.push({ name, status: 'PASS' }); }
  catch (error) { results.push({ name, status: 'FAIL', error: error.message }); }
}
try {
  await page.goto('http://127.0.0.1:5189');
  await button('确认通过').waitFor();
  await check('initial merge disabled', () => expect(button('合并')).toBeDisabled());
  await check('tracked additions render', () => expect(page.locator('pre').first()).toContainText('+  verifySnapshot();'));
  await check('source branch visible', () => expect(page.getByText('feature/local-mr →')).toBeVisible());
  await check('target branch value', () => expect(page.getByRole('combobox', { name: '目标分支' })).toHaveValue('main'));
  await shot('01-open-desktop');
  await check('untracked file selection', async () => { await button('docs/local-review.md').click(); await expect(page.getByText('跨机器查看，在所属运行时合并。', { exact: false })).toBeVisible(); });
  await shot('02-untracked');
  await check('commits expand', async () => { await page.getByText('本地提交', { exact: true }).click(); await expect(page.getByText('abcd1234 Add local MR flow', { exact: true })).toBeVisible(); });
  await check('comment accepts CJK', async () => { const field = page.getByRole('textbox', { name: '审查意见', exact: true }); await field.fill('请覆盖重试场景'); await expect(field).toHaveValue('请覆盖重试场景'); });
  await check('request changes updates status', async () => { await button('请求修改').click(); await expect(page.getByText(/待修改.*abcd1234/)).toBeVisible(); });
  await shot('03-changes-requested');
  await check('merge stays disabled after changes requested', () => expect(button('合并')).toBeDisabled());
  await check('submit reopens review', async () => { await button('提交 MR').click(); await expect(page.getByText(/待审查.*abcd1234/)).toBeVisible(); });
  await check('approve enables merge', async () => { await button('确认通过').click(); await expect(button('合并')).toBeEnabled(); });
  await shot('04-approved');
  await check('merge opens separate confirmation', async () => { await button('合并').click(); await expect(button('确认本地合并')).toBeVisible(); await expect(page.getByText('merged-fixture-commit')).toHaveCount(0); });
  await shot('05-confirm');
  await check('cancel preserves approval', async () => { await button('取消').click(); await expect(button('确认本地合并')).toHaveCount(0); await expect(button('合并')).toBeEnabled(); });
  await check('unapplied target disables decisions', async () => { await page.getByRole('combobox', { name: '目标分支' }).fill('release'); await expect(button('确认通过')).toBeDisabled(); await expect(button('合并')).toBeDisabled(); await expect(button('对比目标分支')).toBeEnabled(); });
  await shot('06-target-draft');
  await check('restoring target restores decisions', async () => { await page.getByRole('combobox', { name: '目标分支' }).fill('main'); await expect(button('合并')).toBeEnabled(); });
  await check('explicit confirmation merges fixture', async () => { await button('合并').click(); await button('确认本地合并').click(); await expect(page.getByText(/merged-fixture-commit/)).toBeVisible(); });
  await check('merged state disables every decision', async () => { for (const name of ['提交 MR', '请求修改', '确认通过', '合并']) await expect(button(name)).toBeDisabled(); });
  await shot('07-merged');
  await check('mobile viewport horizontal bounds', async () => { await page.setViewportSize({ width: 390, height: 844 }); await expect.poll(async () => { const bounds = await page.getByRole('dialog').boundingBox(); return bounds.x >= 0 && bounds.x + bounds.width <= 390; }).toBe(true); });
  await shot('08-mobile');
  await check('no browser runtime exceptions', () => expect(errors).toEqual([]));
  console.log(JSON.stringify({ results, errors }, null, 2));
  await writeFile(`${out}/results.json`, JSON.stringify({ results, errors }, null, 2));
} finally { await browser.close(); }

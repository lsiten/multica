import { chromium, expect } from '@playwright/test';

const browser = await chromium.connectOverCDP('http://127.0.0.1:9337');
try {
  const page = browser.contexts().flatMap(context => context.pages()).find(page => page.url().startsWith('data:text/html'));
  if (!page) throw new Error('isolated Electron fixture page not found');
  await page.getByRole('button', { name: 'Run IPC checks', exact: true }).click();
  await expect(page.locator('#result')).toContainText('"passed":true');
  console.log(await page.locator('#result').textContent());
  await page.screenshot({ path: '.omo/evidence/mr-electron-ipc.png' });
} finally {
  await browser.close();
}

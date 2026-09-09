import { chromium, expect } from '@playwright/test';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';

const out = '.omo/evidence/mr-history';
await mkdir(out, { recursive: true });
const browser = await chromium.launch({ executablePath: '/Users/shicheng_lei/.agent-browser/browsers/chrome-148.0.7778.97/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing' });
const results = [];
try {
  for (const width of [375, 768, 1280]) {
    const page = await browser.newPage({ viewport: { width, height: 900 } });
    await page.goto('http://127.0.0.1:5197/?history');
    await expect(page.getByText('审查记录', { exact: true })).toBeVisible();
    await page.evaluate(() => document.fonts.ready);
    await page.waitForFunction(() => document.getAnimations().every(animation => animation.playState !== 'running'));
    const closed = `${out}/${width}-closed.png`;
    const opened = `${out}/${width}-open.png`;
    await page.screenshot({ path: closed });
    await page.getByText('审查记录', { exact: true }).click();
    await expect(page.getByText(/Reviewer · 已合并/)).toHaveCount(2);
    await page.waitForFunction(() => document.getAnimations().every(animation => animation.playState !== 'running'));
    await page.screenshot({ path: opened });
    for (const path of [closed, opened]) {
      const image = await readFile(path);
      if (image.subarray(0,8).toString('hex') !== '89504e470d0a1a0a' || image.readUInt32BE(16) !== width || image.readUInt32BE(20) !== 900) throw new Error('Invalid capture '+path);
    }
    const { stdout } = await promisify(execFile)('node', ['/Users/shicheng_lei/.codex/plugins/cache/sisyphuslabs/omo/4.19.4/skills/visual-qa/scripts/visual-qa.mjs', 'image-diff', closed, opened]);
    results.push({ width, height:900, completedEvents:2, comparison:'collapsed versus expanded; differences expected', diff:JSON.parse(stdout) });
    await page.close();
  }
  await writeFile(`${out}/results.json`, JSON.stringify(results,null,2));
  console.log('PASS: completed merge history at 375/768/1280, six valid fresh PNGs');
} finally { await browser.close(); }

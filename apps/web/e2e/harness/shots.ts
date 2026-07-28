// Screenshot helper for the DESIGN.md audit: named, full-page shots at the
// two mandated viewports (1280 primary, 1024 functional floor).

import path from "node:path";
import type { Page } from "@playwright/test";
import { screenshotsDir } from "./env";

export async function shoot(page: Page, name: string): Promise<void> {
  await page.screenshot({
    path: path.join(screenshotsDir, `${name}.png`),
    fullPage: true,
  });
}

export async function shootBothViewports(
  page: Page,
  name: string,
): Promise<void> {
  await page.setViewportSize({ width: 1280, height: 800 });
  await shoot(page, `${name}-1280`);
  await page.setViewportSize({ width: 1024, height: 768 });
  await shoot(page, `${name}-1024`);
  await page.setViewportSize({ width: 1280, height: 800 });
}

import { test, expect } from "./vigia";

// Every page in the menu opens and shows its title, without errors. Files
// run in name order: by now nueva-app.spec.ts has made an app of each kind,
// so the pages have something to show.
test("every page in the menu opens", async ({ page, problemas }) => {
  await page.goto("/");
  await expect(page.locator("header.topbar")).toBeVisible();
  const enlaces = await page.locator("header.topbar a.nav-link, header.topbar a.btn").evaluateAll((as) =>
    [...new Set(as.map((a) => a.getAttribute("href") ?? ""))].filter((h) => h.startsWith("/")),
  );
  expect(enlaces.length).toBeGreaterThan(3);
  for (const href of enlaces) {
    await test.step(href, async () => {
      await page.goto(href);
      await expect(page.locator("main h1").first(), `${href} shows a title`).toBeVisible();
    });
  }
  expect(problemas).toEqual([]);
});

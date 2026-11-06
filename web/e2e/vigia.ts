import { test as base, expect, type Page } from "@playwright/test";

/**
 * What every test watches on its page: an error thrown in the browser (a
 * page that crashes renders nothing), an error written to the console, and
 * an API answer of 500. A test fails on any of them, even when what it
 * looked for was there.
 */
export const test = base.extend<{ problemas: string[] }>({
  problemas: async ({ page }, use) => {
    const problemas = vigilar(page);
    await use(problemas);
    expect(problemas, "errors in the browser or the API").toEqual([]);
  },
});

export { expect };

function vigilar(page: Page): string[] {
  const problemas: string[] = [];
  page.on("pageerror", (e) => problemas.push(`${page.url()}: ${e.message}`));
  page.on("console", (m) => {
    // A 4xx answer is logged by the browser too; the API's answers are checked below.
    if (m.type() === "error" && !m.text().startsWith("Failed to load resource")) problemas.push(`${page.url()}: console: ${m.text()}`);
  });
  page.on("response", async (r) => {
    if (r.status() >= 500 && new URL(r.url()).pathname.startsWith("/api/")) {
      problemas.push(`${r.request().method()} ${new URL(r.url()).pathname}: ${r.status()} ${(await r.text().catch(() => "")).slice(0, 300)}`);
    }
  });
  return problemas;
}

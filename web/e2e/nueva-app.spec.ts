import { readdirSync } from "node:fs";
import { test, expect } from "./vigia";

// "New app" with every example of internal/spec/ejemplos: the fake GitHub
// (hack/interfaz) serves each as a repository carrying it as its
// rendimiento.yaml. The wizard shows it, creates the app, and the app's
// page opens. A new kind of app gets its example there, and is walked
// here without a new test.
const ejemplos = readdirSync(new URL("../../internal/spec/ejemplos/", import.meta.url))
  .filter((f) => f.endsWith(".yaml"))
  .map((f) => f.replace(/\.yaml$/, ""));

for (const ejemplo of ejemplos) {
  test(`New app: ${ejemplo}`, async ({ page, problemas }, info) => {
    // Each language makes its own app: names are unique.
    const nombre = `${ejemplo}-${info.project.name}`;
    await page.goto("/new");
    await page.locator("button.ld-item", { has: page.locator(".ld-nombre", { hasText: new RegExp(`^${ejemplo}$`) }) }).click();
    const detalle = page.locator("main.ld-detalle");
    await expect(detalle.locator("code", { hasText: "rendimiento.yaml" }).first(), "the repo's rendimiento.yaml is used").toBeVisible();
    await detalle.locator("label.field input").first().fill(nombre);
    await detalle.locator("button.primary.big").click();
    await expect(detalle.locator("h2.ld-titulo")).toContainText(nombre);
    await detalle.locator(`a.btn.primary[href="/apps/${nombre}"]`).click();
    await expect(page).toHaveURL(new RegExp(`/apps/${nombre}$`));
    await expect(page.locator("main h1").first()).toContainText(nombre);
    // Every tab of the app's page.
    const pestanas = await page.locator(`main a[href^="/apps/${nombre}/"]`).evaluateAll((as) =>
      [...new Set(as.map((a) => a.getAttribute("href") ?? ""))].filter((h) => !h.includes("/runs/")),
    );
    for (const href of pestanas) {
      await test.step(href, async () => {
        await page.goto(href);
        await expect(page.locator("main h1").first()).toContainText(nombre);
      });
    }
    expect(problemas).toEqual([]);
  });
}

// A repository without a rendimiento.yaml: the wizard proposes one (and
// would open a pull request with it; the fake GitHub does not take one).
test("New app: a repo without rendimiento.yaml gets a proposal", async ({ page, problemas }) => {
  await page.goto("/new");
  await page.locator("button.ld-item", { has: page.locator(".ld-nombre", { hasText: /^tienda-node$/ }) }).click();
  const detalle = page.locator("main.ld-detalle");
  await expect(detalle.locator("label.field input").first()).toHaveValue("tienda-node");
  await expect(detalle.locator("button.primary.big")).toBeEnabled();
  await expect(detalle).toContainText("node");
  expect(problemas).toEqual([]);
});

// The browser tests: the real UI and API (hack/interfaz), with an empty
// database and a fake GitHub. hack/ci-interfaz.sh starts the server and
// runs them on every push; INTERFAZ_URL points them elsewhere.
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";

export default defineConfig({
  testDir: ".",
  outputDir: "resultados",
  timeout: 60_000,
  expect: { timeout: 15_000 },
  // One server and one database: the tests run one after another, in order.
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  reporter: [["list"]],
  use: {
    baseURL: process.env.INTERFAZ_URL ?? "http://127.0.0.1:8090",
    storageState: fileURLToPath(new URL(".estado.json", import.meta.url)),
  },
  // English and Spanish: the language follows the browser's.
  projects: [
    { name: "en", use: { locale: "en-US" } },
    { name: "es", use: { locale: "es-MX" } },
  ],
});

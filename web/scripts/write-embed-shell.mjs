import { cp, mkdir, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";

const clientDirectory = new URL("../dist/client/", import.meta.url);
const outputDirectory = new URL("../../server/internal/web/ui/", import.meta.url);
await rm(outputDirectory, { recursive: true, force: true });
await mkdir(outputDirectory, { recursive: true });
await cp(clientDirectory, outputDirectory, { recursive: true });

const { default: server } = await import(new URL("../dist/server/server.js", import.meta.url));
const response = await server.fetch(new Request("http://checkmate.local/", {
  headers: { "x-tss-shell": "true" },
}));
if (!response.ok) throw new Error(`Could not render the TanStack Start shell: ${response.status}.`);
const shell = await response.text();
await writeFile(join(outputDirectory.pathname, "index.html"), shell);

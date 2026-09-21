import { cp, rm, access } from "node:fs/promises";
// Copy only the static SPA, never Start's build-time server bundle.
await access(new URL("../dist/client/index.html", import.meta.url));
const target = new URL("../../internal/webassets/dist/", import.meta.url);
await rm(target, { recursive: true, force: true });
await cp(new URL("../dist/client/", import.meta.url), target, {
  recursive: true,
});

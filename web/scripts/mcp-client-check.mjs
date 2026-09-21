// Cross-language protocol check: the TypeScript SDK validates tools/list and
// structured results independently of the Go SDK used by the server.
import assert from "node:assert/strict";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";

export async function checkMCP(url, token) {
  const client = new Client({ name: "dimsum-compatibility-check", version: "1" });
  const transport = new StreamableHTTPClientTransport(new URL(url), {
    requestInit: { headers: { Authorization: `Bearer ${token}` } },
  });
  try {
    await client.connect(transport);
    const { tools } = await client.listTools();
    assert(tools.length > 0);
    for (const tool of tools) {
      assert.equal(tool.inputSchema.type, "object", `${tool.name} input`);
      if (tool.outputSchema) assert.equal(tool.outputSchema.type, "object", `${tool.name} output`);
    }
    for (const name of ["get_settings", "get_catalog"]) {
      const result = await client.callTool({ name, arguments: {} });
      assert.notEqual(result.isError, true, `${name} failed`);
      assert(result.structuredContent && typeof result.structuredContent === "object");
    }
    return { tools: tools.length, validatedCalls: ["get_settings", "get_catalog"] };
  } finally {
    await client.close();
  }
}

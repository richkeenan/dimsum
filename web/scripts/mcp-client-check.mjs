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
    const validatedCalls = ["get_settings", "get_catalog", "list_clients"];
    for (const name of validatedCalls) {
      const result = await client.callTool({ name, arguments: {} });
      assert.notEqual(result.isError, true, `${name} failed`);
      assert(result.structuredContent && typeof result.structuredContent === "object");
    }
    return { tools: tools.length, validatedCalls };
  } finally {
    await client.close();
  }
}

// Kept beside the compatibility check so repository-level browser specs resolve
// the same pinned SDK as the frontend. callTool validates structured responses.
export async function connectDHCPAgent(url, token) {
  const client = new Client({ name: "dimsum-dhcp-parity", version: "1" });
  await client.connect(
    new StreamableHTTPClientTransport(new URL(url), {
      requestInit: { headers: { Authorization: `Bearer ${token}` } },
    }),
  );
  const { tools } = await client.listTools();
  for (const name of [
    "get_dhcp",
    "update_dhcp",
    "get_dhcp_status",
    "list_dhcp_leases",
    "list_dhcp_reservations",
    "add_dhcp_reservation",
    "update_dhcp_reservation",
    "remove_dhcp_reservation",
  ])
    assert(
      tools.some((t) => t.name === name),
      `${name} advertised`,
    );
  return client;
}

export function checkMCP(
  url: string,
  token: string,
): Promise<{
  tools: number;
  validatedCalls: string[];
}>;
export function connectDHCPAgent(
  url: string,
  token: string,
): Promise<import("@modelcontextprotocol/sdk/client/index.js").Client>;

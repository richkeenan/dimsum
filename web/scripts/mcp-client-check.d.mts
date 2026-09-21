export function checkMCP(url: string, token: string): Promise<{
  tools: number;
  validatedCalls: string[];
}>;

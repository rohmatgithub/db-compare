import { errorFrom } from "./api";

export type StreamEvent = { name: string; data: any };

/**
 * POSTs to an endpoint that answers with Server-Sent Events and calls onEvent
 * for each event until the stream ends. EventSource cannot send a POST body,
 * so the stream is parsed by hand. Aborting the signal closes the request,
 * which makes the server cancel the work.
 */
export async function streamEvents(
  url: string,
  body: unknown,
  onEvent: (event: StreamEvent) => void,
  signal: AbortSignal,
): Promise<void> {
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
    body: JSON.stringify(body ?? {}),
    signal,
    cache: "no-store",
  });
  if (!res.ok) throw await errorFrom(res);
  if (!res.body) throw new Error("the server returned an empty stream");

  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buffer = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += value;
    let boundary: number;
    while ((boundary = buffer.indexOf("\n\n")) >= 0) {
      const block = buffer.slice(0, boundary);
      buffer = buffer.slice(boundary + 2);
      const event = parseBlock(block);
      if (event) onEvent(event);
    }
  }
}

function parseBlock(block: string): StreamEvent | null {
  let name = "message";
  const data: string[] = [];
  for (const line of block.split("\n")) {
    if (line.startsWith("event:")) name = line.slice(6).trim();
    else if (line.startsWith("data:")) data.push(line.slice(5).trimStart());
  }
  if (data.length === 0) return null;
  try {
    return { name, data: JSON.parse(data.join("\n")) };
  } catch {
    return { name, data: data.join("\n") };
  }
}

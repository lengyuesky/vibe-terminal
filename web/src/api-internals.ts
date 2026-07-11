export function jsonRequestHeaders(init?: HeadersInit): Headers {
  const headers = new Headers(init);
  if (!headers.has('Content-Type')) headers.set('Content-Type', 'application/json');
  return headers;
}

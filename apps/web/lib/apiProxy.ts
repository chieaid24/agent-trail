export const apiProxyPrefix = "/backend";

// read per request, not at build, so the container's API_PROXY_TARGET wins
export function apiProxyTarget(): string {
  return (process.env.API_PROXY_TARGET ?? "http://localhost:8080").replace(
    /\/+$/,
    "",
  );
}

export function apiProxyUrl(url: URL): URL {
  return new URL(
    `${apiProxyTarget()}${url.pathname.slice(apiProxyPrefix.length)}${url.search}`,
  );
}

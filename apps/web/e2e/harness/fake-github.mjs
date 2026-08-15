// Minimal fake of the two GitHub hosts the auth flow touches: github.com
// (authorize redirect, code exchange) and api.github.com (user, user
// installations). Runs as a harness daemon; the api points both base URLs
// here. Account 118775203 mirrors cmd/seed so membership sync matches the
// seeded organization.

import http from "node:http";

const port = Number(process.argv[2]);
if (!Number.isInteger(port) || port <= 0) {
  console.error("usage: node fake-github.mjs <port>");
  process.exit(1);
}

function json(res, body) {
  res.writeHead(200, { "Content-Type": "application/json" });
  res.end(JSON.stringify(body));
}

const server = http.createServer((req, res) => {
  const url = new URL(req.url, `http://127.0.0.1:${port}`);
  if (req.method === "GET" && url.pathname === "/login/oauth/authorize") {
    const redirect = new URL(url.searchParams.get("redirect_uri"));
    redirect.searchParams.set("code", "e2e-code");
    redirect.searchParams.set("state", url.searchParams.get("state") ?? "");
    res.writeHead(302, { Location: redirect.toString() });
    res.end();
    return;
  }
  if (req.method === "POST" && url.pathname === "/login/oauth/access_token") {
    json(res, { access_token: "e2e-user-token", token_type: "bearer" });
    return;
  }
  if (req.method === "GET" && url.pathname === "/user") {
    json(res, {
      id: 424242,
      login: "e2e-octocat",
      name: "E2E Octocat",
      avatar_url: "",
    });
    return;
  }
  if (req.method === "GET" && url.pathname === "/user/installations") {
    json(res, {
      installations: [
        {
          id: 90001,
          account: { id: 118775203, login: "chieaid24", type: "User" },
        },
      ],
    });
    return;
  }
  res.writeHead(404, { "Content-Type": "application/json" });
  res.end(JSON.stringify({ message: "not found" }));
});

server.listen(port, "127.0.0.1");

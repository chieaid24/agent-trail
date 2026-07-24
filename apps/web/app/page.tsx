import Link from "next/link";

const repoUrl = "https://github.com/chieaid24/agent-trail";

export default function Home() {
  return (
    <div className="flex min-h-screen">
      <aside className="flex w-56 shrink-0 flex-col border-r border-border bg-surface px-4 py-6">
        <span className="text-base font-semibold tracking-tight">
          Agent Trail
        </span>
        <nav aria-label="Primary" className="mt-8">
          <Link
            href="/"
            aria-current="page"
            className="block rounded px-2 py-1.5 text-sm font-semibold"
          >
            Tasks
          </Link>
        </nav>
      </aside>
      <main className="flex flex-1 items-center justify-center p-8">
        <div className="max-w-sm text-center">
          <p className="text-sm text-muted">
            No tasks yet. Comment{" "}
            <code className="font-mono text-foreground">/agent-trail run</code>{" "}
            on a GitHub issue to start one.
          </p>
          <a
            href={repoUrl}
            className="mt-4 inline-block text-sm text-accent underline-offset-4 hover:underline"
          >
            Read the docs
          </a>
        </div>
      </main>
    </div>
  );
}

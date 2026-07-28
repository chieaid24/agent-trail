"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

// Fixed-sidebar app shell (DESIGN.md container policy). Every screen
// renders inside it; the main region owns scrolling.
export function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname() ?? "/";
  const onTasks = pathname === "/" || pathname.startsWith("/tasks");
  return (
    <div className="flex min-h-screen">
      <aside className="fixed inset-y-0 left-0 flex w-56 flex-col border-r border-border bg-surface px-4 py-6">
        <Link
          href="/"
          className="text-base font-semibold tracking-tight text-foreground"
        >
          Agent Trail
        </Link>
        <nav aria-label="Primary" className="mt-8">
          <Link
            href="/"
            aria-current={onTasks ? "page" : undefined}
            className={`block rounded px-2 py-1.5 text-sm font-semibold ${
              onTasks ? "text-foreground" : "text-muted hover:text-foreground"
            }`}
          >
            Tasks
          </Link>
        </nav>
        <p className="mt-auto text-sm text-muted">local control plane</p>
      </aside>
      <main className="ml-56 min-w-0 flex-1">{children}</main>
    </div>
  );
}

"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import { ApiError, getMe, logout } from "@/lib/api";
import type { CurrentUser } from "@/lib/types";

// The session user, or null while loading, signed out, or with auth not
// configured (a /me 503); the chip simply hides in the null states and
// api.ts redirects 401s to /login.
function useCurrentUser(): CurrentUser | null {
  const [user, setUser] = useState<CurrentUser | null>(null);
  useEffect(() => {
    let cancelled = false;
    getMe()
      .then((me) => {
        // Guard the shape: a proxy misroute must degrade, not crash.
        if (!cancelled && typeof me?.user?.github_login === "string") {
          setUser(me.user);
        }
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);
  return user;
}

// Fixed-sidebar app shell (DESIGN.md container policy). Every screen
// renders inside it; the main region owns scrolling.
export function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname() ?? "/";
  const user = useCurrentUser();
  const items = [
    {
      href: "/",
      label: "Tasks",
      current: pathname === "/" || pathname.startsWith("/tasks"),
    },
    {
      href: "/installations",
      label: "Installations",
      current: pathname.startsWith("/installations"),
    },
  ];
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
          {items.map((item) => (
            <Link
              key={item.href}
              href={item.href}
              aria-current={item.current ? "page" : undefined}
              className={`block rounded px-2 py-1.5 text-sm font-semibold ${
                item.current
                  ? "text-foreground"
                  : "text-muted hover:text-foreground"
              }`}
            >
              {item.label}
            </Link>
          ))}
        </nav>
        <div className="mt-auto">
          {user === null ? (
            <p className="text-sm text-muted">local control plane</p>
          ) : (
            <UserChip user={user} />
          )}
        </div>
      </aside>
      <main className="ml-56 min-w-0 flex-1">{children}</main>
    </div>
  );
}

function UserChip({ user }: { user: CurrentUser }) {
  const [error, setError] = useState("");
  return (
    <div>
      <div className="flex items-center gap-2">
        {user.avatar_url !== "" && (
          // GitHub-hosted avatar; the image pipeline buys nothing at 24px.
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={user.avatar_url}
            alt=""
            width={24}
            height={24}
            className="h-6 w-6 rounded-full border border-border"
          />
        )}
        <span className="min-w-0 truncate font-mono text-sm">
          {user.github_login}
        </span>
      </div>
      {error !== "" && (
        <p className="mt-1 text-sm text-danger">Sign out failed: {error}.</p>
      )}
      <button
        type="button"
        onClick={async () => {
          try {
            await logout();
            window.location.assign("/login");
          } catch (err) {
            setError(
              err instanceof ApiError ? err.message : "unexpected failure",
            );
          }
        }}
        className="mt-2 text-sm text-muted hover:text-foreground"
      >
        Sign out
      </button>
    </div>
  );
}

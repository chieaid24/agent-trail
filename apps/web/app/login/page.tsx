"use client";

import { Suspense } from "react";
import { useSearchParams } from "next/navigation";
import { LOGIN_URL } from "@/lib/api";

const errorMessages: Record<string, string> = {
  state_mismatch: "The sign-in attempt expired or did not match. Try again.",
  github_denied: "GitHub denied the authorization request.",
  missing_code: "GitHub returned no sign-in code. Try again.",
  login_failed: "Signing in failed on the control plane. Try again.",
};

export default function LoginPage() {
  return (
    <Suspense>
      <Login />
    </Suspense>
  );
}

function Login() {
  const error = useSearchParams()?.get("error") ?? null;
  return (
    <main className="flex min-h-screen items-center justify-center px-8">
      <div className="w-full max-w-sm text-center">
        <h1 className="text-xl font-semibold tracking-tight">Agent Trail</h1>
        <p className="mt-2 text-sm text-muted">
          Sign in to see what your agents are doing.
        </p>
        {error !== null && (
          <p className="mt-6 text-sm text-danger" role="alert">
            {errorMessages[error] ?? "Signing in failed. Try again."}
          </p>
        )}
        <a
          href={LOGIN_URL}
          className="mt-8 inline-flex items-center gap-2 rounded bg-accent px-4 py-2 text-sm font-semibold text-background"
        >
          <GitHubMark />
          Sign in with GitHub
        </a>
      </div>
    </main>
  );
}

function GitHubMark() {
  return (
    <svg aria-hidden viewBox="0 0 16 16" className="h-4 w-4 fill-current">
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27s1.36.09 2 .27c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z" />
    </svg>
  );
}

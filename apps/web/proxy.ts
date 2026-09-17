import { NextResponse, type NextRequest } from "next/server";
import { apiProxyUrl } from "@/lib/apiProxy";

// browser traffic stays on the dashboard origin; the server forwards /backend/* to the API
export function proxy(request: NextRequest): NextResponse {
  return NextResponse.rewrite(apiProxyUrl(request.nextUrl));
}

export const config = { matcher: "/backend/:path*" };

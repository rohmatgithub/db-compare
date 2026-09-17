"use client";

import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { ApiError } from "@/lib/api";
import { redirectToLogin } from "@/lib/auth";

// A 401 means the session is missing or expired, so any failing request
// sends the user to the sign-in page.
function onError(err: unknown) {
  if (err instanceof ApiError && err.status === 401) redirectToLogin();
}

export function Providers({ children }: { children: ReactNode }) {
  const [client] = useState(
    () =>
      new QueryClient({
        queryCache: new QueryCache({ onError }),
        mutationCache: new MutationCache({ onError }),
        defaultOptions: {
          queries: {
            // Client errors such as 401 or 403 do not change on retry.
            retry: (count, err) => !(err instanceof ApiError && err.status < 500) && count < 1,
            refetchOnWindowFocus: false,
          },
        },
      }),
  );
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

"use client";

import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../i18n";

export function SearchFailure({ retry }: { readonly retry: () => void }) {
  const { t } = useT("search");
  return <div role="alert" className="flex items-center justify-between gap-3 px-4 py-3">
    <p className="text-body text-destructive">{t(($) => $.empty.search_failed)}</p>
    <Button variant="outline" size="sm" onClick={retry}>{t(($) => $.empty.retry)}</Button>
  </div>;
}

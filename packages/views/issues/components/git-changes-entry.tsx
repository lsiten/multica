import type { LocalReviewRequest } from "@multica/core/types/local-review";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";

export function GitChangesEntry({ request, onOpen, className, labelSuffix }: {
  readonly request: LocalReviewRequest;
  readonly onOpen: (request: LocalReviewRequest) => void;
  readonly className?: string;
  readonly labelSuffix?: string;
}) {
  const { t } = useT("issues");
  return <Button className={className} variant="outline" size="sm" onClick={() => onOpen(request)}>{t(($) => $.git_changes.open)}{labelSuffix ? ` · ${labelSuffix}` : ""}</Button>;
}

import { useT } from "../i18n";

export function useApplicationActionLabels() {
  const { t } = useT("applications");
  return { start: t(($) => $.start), stop: t(($) => $.stop), restart: t(($) => $.restart), publish: t(($) => $.publish), unpublish: t(($) => $.unpublish), delete: t(($) => $.delete_instance) };
}

import { useState } from "react";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { SettingsCard, SettingsRow, SettingsTab } from "@multica/views/settings";
import { useT } from "@multica/views/i18n";
import { DEFAULT_RUNTIME_CONFIG, parseRuntimeConfig } from "../../../shared/runtime-config";

export function AppSettingsTab() {
  const { t } = useT("settings");
  const initial = window.desktopAPI.runtimeConfig.ok
    ? window.desktopAPI.runtimeConfig.config
    : DEFAULT_RUNTIME_CONFIG;
  const [apiUrl, setApiUrl] = useState(initial.apiUrl);
  const [appName, setAppName] = useState(initial.appName);
  const [iconPath, setIconPath] = useState(initial.iconPath ?? "");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);

  async function save() {
    setSaving(true);
    setError("");
    setSaved(false);
    try {
      const config = parseRuntimeConfig(JSON.stringify({
        schemaVersion: 1,
        apiUrl,
        appName,
        ...(apiUrl.trim().replace(/\/+$/, "") === initial.apiUrl
          ? { wsUrl: initial.wsUrl, appUrl: initial.appUrl } : {}),
        ...(iconPath ? { iconPath } : {}),
      }));
      await window.desktopAPI.saveRuntimeConfig(config);
      setSaved(true);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t(($) => $.desktop.application.save_failed));
    } finally {
      setSaving(false);
    }
  }

  async function pickIcon() {
    setError("");
    try {
      const selected = await window.desktopAPI.pickAppIcon();
      if (selected) { setIconPath(selected); setSaved(false); }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t(($) => $.desktop.application.icon_failed));
    }
  }

  return (
    <SettingsTab title={t(($) => $.desktop.application.title)}>
      <SettingsCard>
        <SettingsRow label={<label htmlFor="desktop-api-url">{t(($) => $.desktop.application.api_url)}</label>} size="text"
          description={t(($) => $.desktop.application.api_description)}>
          <Input id="desktop-api-url" value={apiUrl} onChange={(event) => { setApiUrl(event.target.value); setSaved(false); }} disabled={saving} />
        </SettingsRow>
        <SettingsRow label={<label htmlFor="desktop-app-name">{t(($) => $.desktop.application.app_name)}</label>} size="text">
          <Input id="desktop-app-name" value={appName} maxLength={64} onChange={(event) => { setAppName(event.target.value); setSaved(false); }} disabled={saving} />
        </SettingsRow>
        <SettingsRow label={t(($) => $.desktop.application.icon)} size="text">
          <div className="space-y-2">
            <div className="flex gap-2">
              <Button variant="outline" size="sm" onClick={() => void pickIcon()} disabled={saving}>{t(($) => $.desktop.application.choose_icon)}</Button>
              {iconPath && <Button variant="ghost" size="sm" onClick={() => { setIconPath(""); setSaved(false); }} disabled={saving}>{t(($) => $.desktop.application.reset_icon)}</Button>}
            </div>
            <p className="break-all text-caption text-muted-foreground">{iconPath || t(($) => $.desktop.application.default_icon)}</p>
          </div>
        </SettingsRow>
      </SettingsCard>
      {error && <p role="alert" className="text-body text-destructive">{error}</p>}
      <div className="flex items-center gap-3">
        <Button onClick={() => void save()} disabled={saving} aria-busy={saving}>{t(($) => $.desktop.application.save)}</Button>
        {saved && <><span role="status" className="text-body text-muted-foreground">{t(($) => $.desktop.application.saved)}</span><Button variant="outline" onClick={() => void window.desktopAPI.restartApp()}>{t(($) => $.desktop.application.restart)}</Button></>}
      </div>
    </SettingsTab>
  );
}

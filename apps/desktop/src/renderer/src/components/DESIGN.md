# Desktop application settings

Application settings reuse the shared settings component system in
`packages/views/settings/components/settings-layout.tsx` and semantic tokens in
`packages/ui/styles/tokens.css`.

- Keep the existing settings shell and its scroll owner. Use SettingsTab,
  SettingsCard and SettingsRow with the standard text control width.
- Use title-lg, body and caption typography and existing surface, foreground,
  muted-foreground and destructive tokens.
- Label text inputs explicitly. Native file selection is cancellable; errors
  remain visible through role=alert. Save is disabled and busy while writing.
- Save persists configuration; a separate restart action applies it. Endpoint
  changes derive WebSocket and web endpoints; branding-only edits preserve
  existing explicit endpoint overrides.
- Show successful save through role=status. Editing again hides restart until
  the new draft is saved. Do not add custom animation or new primitives.
- Branding changes the running application name and dock/window icon. Installed
  bundle names and Finder icons are outside this settings contract.
- Backend-specific Electron session partitions isolate credentials and storage.
  Existing unpartitioned sessions require a fresh login after this upgrade.

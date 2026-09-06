import { useId } from "react";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";

// Webhook URLs are credentials too. Keep the masking and autocomplete policy
// identical for all secret fields instead of relying on each form call site.
export function NotificationBotSecretInput({ label, value, onValueChange, maxLength = 256, required = false }: {
  readonly label: string;
  readonly value: string;
  readonly onValueChange: (value: string) => void;
  readonly maxLength?: number;
  readonly required?: boolean;
}) {
  const id = useId();
  return <div className="space-y-2">
    <Label htmlFor={id}>{label}</Label>
    <Input id={id} type="password" autoComplete="new-password" required={required} maxLength={maxLength} value={value} onChange={(event) => onValueChange(event.target.value)} />
  </div>;
}

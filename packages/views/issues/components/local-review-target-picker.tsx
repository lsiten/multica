import { useState } from "react";
import { ChevronsUpDown } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@multica/ui/components/ui/popover";
import { Command, CommandEmpty, CommandInput, CommandItem, CommandList } from "@multica/ui/components/ui/command";
import { useT } from "../../i18n";

type Props = {
  branches: readonly string[];
  value: string;
  onChange: (value: string) => void;
  disabled: boolean;
  invalid: boolean;
  descriptionId?: string;
};

export function LocalReviewTargetPicker({ branches, value, onChange, disabled, invalid, descriptionId }: Props) {
  const { t } = useT("issues");
  const [open, setOpen] = useState(false);
  return <Popover open={open} onOpenChange={setOpen}>
    <PopoverTrigger render={<Button variant="outline" role="combobox" aria-label={t(($) => $.local_review.target)} aria-expanded={open} aria-invalid={invalid} aria-describedby={descriptionId} disabled={disabled} className="w-48 justify-between" />}>
      <span className="min-w-0 truncate">{value || t(($) => $.local_review.target)}</span>
      <ChevronsUpDown aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
    </PopoverTrigger>
    <PopoverContent align="start" className="w-80 max-w-[calc(100vw-2rem)] p-0">
      <Command label={t(($) => $.local_review.search_branches)}>
        <CommandInput aria-label={t(($) => $.local_review.search_branches)} placeholder={t(($) => $.local_review.search_branches)} />
        <CommandList>
          <CommandEmpty>{t(($) => $.local_review.no_matching_branches)}</CommandEmpty>
          {branches.map((branch) => <CommandItem key={branch} value={branch} onSelect={() => { onChange(branch); setOpen(false); }}><span className="break-all">{branch}</span></CommandItem>)}
        </CommandList>
      </Command>
    </PopoverContent>
  </Popover>;
}

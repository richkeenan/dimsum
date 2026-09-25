import { DynamicIcon, iconNames, type IconName } from "lucide-react/dynamic";
import { Monitor, type LucideIcon, type LucideProps } from "lucide-react";
import { useId } from "react";
import { Input } from "@/components/ui/input";

const names = new Set<string>(iconNames);
export function isDeviceIcon(name: string): name is IconName {
  return names.has(name);
}

export function DeviceIcon({
  name,
  fallback: Fallback = Monitor,
  ...props
}: LucideProps & { name?: string; fallback?: LucideIcon }) {
  if (!name || !isDeviceIcon(name)) return <Fallback {...props} />;
  return <DynamicIcon key={name} name={name} fallback={() => <Fallback {...props} />} {...props} />;
}

export function DeviceIconField({
  value,
  onChange,
}: {
  value: string;
  onChange: (value: string) => void;
}) {
  const id = useId();
  const valid = !value || isDeviceIcon(value);
  const suggestions = iconNames.filter((name) => name.includes(value)).slice(0, 30);
  return (
    <div className="space-y-1 text-sm">
      <label htmlFor={id}>Icon</label>
      <div className="flex items-center gap-2">
        <DeviceIcon
          name={value}
          className="size-5 shrink-0 text-muted-foreground"
          aria-hidden="true"
        />
        <Input
          id={id}
          value={value}
          list={`${id}-names`}
          placeholder="Automatic"
          maxLength={80}
          autoComplete="off"
          aria-invalid={!valid}
          aria-describedby={`${id}-help`}
          onChange={(e) => {
            const next = e.target.value;
            e.target.setCustomValidity(
              !next || isDeviceIcon(next) ? "" : "Choose a known Lucide icon name.",
            );
            onChange(next);
          }}
        />
        <datalist id={`${id}-names`}>
          {suggestions.map((name) => (
            <option key={name} value={name} />
          ))}
        </datalist>
      </div>
      <p
        id={`${id}-help`}
        className={`text-xs ${valid ? "text-muted-foreground" : "text-destructive"}`}
      >
        {valid
          ? "Lucide name, e.g. washing-machine or bell. Leave blank for automatic."
          : "Unknown Lucide icon name."}
      </p>
    </div>
  );
}

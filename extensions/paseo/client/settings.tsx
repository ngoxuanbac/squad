import { useSettings, type PluginSurfaceProps } from "@getpaseo/plugin/client";
import { SettingsAction, SettingsCard, SettingsInput, SettingsRow, SettingsSection } from "@getpaseo/plugin/client/ui";
import { useState } from "react";
import { settings } from "../shared/rpc";

/** Settings: which squad binary the Paseo daemon runs. */
export function SquadSettings(_props: PluginSurfaceProps) {
  const state = useSettings(settings);
  const [draft, setDraft] = useState<string | null>(null);
  if (state.status === "loading") {
    return (
      <SettingsSection title="Squad">
        <SettingsCard>
          <SettingsRow label="Loading…" />
        </SettingsCard>
      </SettingsSection>
    );
  }
  if (state.status !== "ready") {
    return (
      <SettingsSection title="Squad">
        <SettingsCard>
          <SettingsAction label="The stored settings cannot be read" error={state.error} actionLabel="Reset" onPress={() => void state.reset()} />
        </SettingsCard>
      </SettingsSection>
    );
  }
  const value = (draft ?? state.values.path).trim();
  const changed = value !== state.values.path;
  return (
    <SettingsSection title="Squad">
      <SettingsCard>
        <SettingsInput
          key={state.revision}
          label="squad binary"
          hint="A full path to squad (squad.exe on Windows). Leave it empty to use the squad that installed this plugin, else squad on the Paseo daemon's PATH."
          initialValue={state.values.path}
          placeholder="squad"
          onChangeText={setDraft}
          error={state.saveError}
        />
        <SettingsAction
          label="Apply"
          hint="The next read uses it."
          actionLabel={state.saving ? "Saving…" : "Save"}
          disabled={!changed || state.saving}
          onPress={async () => {
            if (await state.save({ path: value }, state.revision)) setDraft(null);
          }}
        />
      </SettingsCard>
    </SettingsSection>
  );
}

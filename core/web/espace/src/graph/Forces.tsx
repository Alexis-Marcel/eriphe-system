import { useWords } from "../i18n";
import { DEFAULT_FORCES, FORCE_RANGES, type Forces } from "./layout";

// Forces is the graph's four forces as sliders, like Obsidian's « Forces »
// panel, and a « Par défaut » that gives the defaults back. It only shows
// and tells: value is the forces now (layout.ts, forcesFrom: the defaults
// and what the user changed), onChange gets the new ones at each move
// (the caller sets them, setForces, and keeps them); centred, whether the
// open node becomes the centre (layout.ts, CENTRING), told by onCentred
// (no switch without it). The workspace's settings show it
// (hud/Settings.tsx).

const ORDER: (keyof Forces)[] = ["centre", "repulsion", "link", "distance"];

export function ForcesPanel({ value, onChange, centred, onCentred }: { value: Forces; onChange: (f: Forces) => void; centred?: boolean; onCentred?: (on: boolean) => void }) {
  const { t } = useWords();
  const same = ORDER.every((k) => value[k] === DEFAULT_FORCES[k]);
  return (
    <fieldset className="forces">
      <legend>{t("espace.forces")}</legend>
      {ORDER.map((k) => {
        const r = FORCE_RANGES[k];
        return (
          <label key={k} className="forces-row">
            <span>
              {t(`espace.forces.${k}`)}
              <output>{value[k]}</output>
            </span>
            <input type="range" min={r.min} max={r.max} step={r.step} value={value[k]} onChange={(e) => onChange({ ...value, [k]: Number(e.target.value) })} />
          </label>
        );
      })}
      {onCentred && (
        <label className="forces-switch">
          <input type="checkbox" checked={!!centred} onChange={(e) => onCentred(e.target.checked)} />
          {t("espace.forces.focus")}
        </label>
      )}
      <button type="button" className="btn forces-reset" disabled={same} onClick={() => onChange({ ...DEFAULT_FORCES })}>
        {t("espace.forces.reset")}
      </button>
    </fieldset>
  );
}

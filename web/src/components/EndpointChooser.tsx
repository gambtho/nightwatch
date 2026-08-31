import { ENDPOINT_PRESETS, type PresetId } from "../local/endpoints";

// "Choose where your AI runs" — the five hosted presets plus "another
// service" and "on this computer", each a single click into its capture
// card. Copy over configuration: the blurb says what the choice means,
// not what protocol it speaks.

export default function EndpointChooser({
  onChoose,
}: {
  onChoose: (id: PresetId) => void;
}) {
  return (
    <div className="endpoint-chooser">
      {ENDPOINT_PRESETS.map((preset) => (
        <button
          key={preset.id}
          type="button"
          className="endpoint-option"
          onClick={() => onChoose(preset.id)}
        >
          <span className="endpoint-option-label">{preset.label}</span>
          <span className="dim">{preset.blurb}</span>
        </button>
      ))}
    </div>
  );
}

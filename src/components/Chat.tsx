import type { BuildTurn } from "../fixtures/conversation";

export default function Chat({ turns }: { turns: BuildTurn[] }) {
  return (
    <div className="chat">
      {turns.map((t) => (
        <div key={t.id} className={`bubble bubble-${t.speaker}`}>
          {t.text}
        </div>
      ))}
    </div>
  );
}

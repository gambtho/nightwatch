import { useEffect, useState } from "react";
import type { CatalogConnector } from "../api/types";
import CaptureCard from "../components/CaptureCard";
import { opLabel } from "../lib/catalog";
import { useCatalog } from "../lib/useCatalog";
import {
  captureGuideFor,
  connectWithToken,
  connectionState,
  disconnect,
  listMcpServers,
  registerMcpServer,
  removeMcpServer,
  stateOverlay,
  stateView,
  type ConnectionState,
  type McpServer,
  type StateOverlay,
} from "../local/connections";
import "./screens.css";
import "./connections.css";

// The standalone connections manager (pivot spec, "Credentials without
// OAuth"): connections are added once, here, and every subsequent build
// finds them already connected. One screen lists every catalog connector
// and registered MCP server with its state; this surface owns the
// capture cards, MCP registration, re-paste on revocation, and
// disconnect — build conversations link into it and return when a card
// lands ok. Ops render with their read/write effect, the state the
// post-verdict inputs/outputs palette will need.
//
// The catalog is real (GET /v1/catalog); states beyond its `connected`
// boolean, the capture guides, and connect/disconnect are the
// local/connections.ts fake seam until connectors P2.

function StateBadge({ state }: { state: ConnectionState }) {
  const view = stateView(state);
  return (
    <span className={`wf-badge ${view.tone === "ok" ? "wf-badge-ok" : "wf-badge-draft"}`}>
      {view.label}
    </span>
  );
}

function ConnectorRow({
  connector,
  state,
  onConnected,
  onDisconnected,
}: {
  connector: CatalogConnector;
  state: ConnectionState;
  onConnected: () => void;
  onDisconnected: () => void;
}) {
  const [capturing, setCapturing] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const guide = captureGuideFor(connector);

  async function doDisconnect() {
    setBusy(true);
    await disconnect(connector.id);
    setBusy(false);
    setConfirming(false);
    onDisconnected();
  }

  return (
    <div className="conn-row">
      <div className="conn-head">
        <span className="conn-name">{connector.name}</span>
        <StateBadge state={state} />
        {state === "ok" && !confirming && (
          <button className="btn-quiet" onClick={() => setConfirming(true)}>
            Disconnect
          </button>
        )}
        {state !== "ok" && !capturing && (
          <button className="btn btn-secondary" onClick={() => setCapturing(true)}>
            {state === "needs_reauth" ? "Paste a fresh key…" : "Connect…"}
          </button>
        )}
      </div>
      <p className="dim conn-desc">{connector.description}</p>
      <div className="conn-ops">
        {connector.ops.map((op) => (
          <span key={op.name} className="conn-op">
            {opLabel(op.name)}{" "}
            <span className={`setup-effect setup-effect-${op.effect}`}>{op.effect}</span>
          </span>
        ))}
      </div>

      {confirming && (
        <div className="conn-confirm">
          <p className="dim">
            Workflows that reach {connector.name} will start failing until it's
            reconnected.
          </p>
          <button className="btn" onClick={() => void doDisconnect()} disabled={busy}>
            {busy ? "Disconnecting…" : "Disconnect"}
          </button>
          <button
            className="btn-quiet"
            onClick={() => setConfirming(false)}
            disabled={busy}
          >
            Keep it
          </button>
        </div>
      )}

      {capturing && (
        <div className="conn-capture">
          <CaptureCard
            title={`Connect ${connector.name}`}
            steps={guide.steps}
            startUrl={guide.startUrl}
            startLabel={guide.startLabel}
            placeholder={guide.placeholder}
            secretLabel="Token"
            verifyLabel={state === "needs_reauth" ? "Verify and reconnect" : "Connect"}
            checkShape={(token) =>
              guide.secretPrefix && !token.startsWith(guide.secretPrefix)
                ? `That doesn't look right — the token should start with ${guide.secretPrefix}.`
                : null
            }
            onVerify={(token) => connectWithToken(connector.id, token)}
            onVerified={() => {
              setCapturing(false);
              onConnected();
            }}
          />
          <button className="btn-quiet" onClick={() => setCapturing(false)}>
            Not now
          </button>
        </div>
      )}
    </div>
  );
}

function McpRow({ server, onRemoved }: { server: McpServer; onRemoved: () => void }) {
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);

  async function remove() {
    setBusy(true);
    await removeMcpServer(server.id);
    setBusy(false);
    setConfirming(false);
    onRemoved();
  }

  return (
    <div className="conn-row">
      <div className="conn-head">
        <span className="conn-name">{server.name}</span>
        <StateBadge state={server.state} />
        {!confirming && (
          <button className="btn-quiet" onClick={() => setConfirming(true)}>
            Remove
          </button>
        )}
      </div>
      <p className="dim conn-desc">{server.url}</p>
      {confirming && (
        <div className="conn-confirm">
          <p className="dim">
            Workflows that reach {server.name} will start failing until it's added again.
          </p>
          <button className="btn" onClick={() => void remove()} disabled={busy}>
            {busy ? "Removing…" : "Remove"}
          </button>
          <button
            className="btn-quiet"
            onClick={() => setConfirming(false)}
            disabled={busy}
          >
            Keep it
          </button>
        </div>
      )}
    </div>
  );
}

export default function Connections() {
  const catalog = useCatalog();
  const [overlay, setOverlay] = useState<StateOverlay | null>(null);
  const [mcpServers, setMcpServers] = useState<McpServer[] | null>(null);
  const [addingMcp, setAddingMcp] = useState(false);
  const [mcpName, setMcpName] = useState("");
  const [mcpUrl, setMcpUrl] = useState("");

  useEffect(() => {
    let cancelled = false;
    stateOverlay().then((o) => {
      if (!cancelled) setOverlay(o);
    });
    listMcpServers().then((servers) => {
      if (!cancelled) setMcpServers(servers);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  async function refresh() {
    setOverlay(await stateOverlay());
    setMcpServers(await listMcpServers());
  }

  return (
    <div className="screen">
      <h1>Connections</h1>
      <p className="dim conn-intro">
        Connect a tool once, here, and every job you build finds it ready. Each tool shows
        what it can be asked to do —{" "}
        <span className="setup-effect setup-effect-read">read</span> means look at things,{" "}
        <span className="setup-effect setup-effect-write">write</span> means change them —
        and a workflow still only reaches what you approve for it.
      </p>

      <section className="conn-section">
        <div className="label">Tools Tomte knows how to reach</div>
        {catalog === undefined && <p className="dim">Loading…</p>}
        {catalog === null && (
          <p className="error-note">
            The catalog couldn't be loaded, so nothing can be connected right now. Refresh
            to try again.
          </p>
        )}
        {catalog !== undefined &&
          catalog !== null &&
          overlay !== null &&
          catalog.map((connector) => (
            <ConnectorRow
              key={connector.id}
              connector={connector}
              state={connectionState(connector, overlay)}
              onConnected={() => void refresh()}
              onDisconnected={() => void refresh()}
            />
          ))}
      </section>

      <section className="conn-section">
        <div className="label">MCP servers</div>
        <p className="dim conn-intro">
          A tool vendor can offer a connection point of their own — they'll give you an
          address and a key. Their sign-in, if any, happens on their site; Tomte only
          holds the key they issue.
        </p>
        {mcpServers !== null && mcpServers.length === 0 && !addingMcp && (
          <p className="dim">None registered yet.</p>
        )}
        {mcpServers?.map((server) => (
          <McpRow key={server.id} server={server} onRemoved={() => void refresh()} />
        ))}
        {!addingMcp ? (
          <button className="btn btn-secondary" onClick={() => setAddingMcp(true)}>
            + Add an MCP server
          </button>
        ) : (
          <div className="conn-capture">
            <CaptureCard
              title="Register an MCP server"
              steps={[
                "Find the connection details the vendor gives you — an address and a key.",
                "Paste the address above and the key below.",
              ]}
              secretLabel="Key"
              placeholder="the key the vendor issued"
              verifyLabel="Register"
              onVerify={(key) => registerMcpServer(mcpName, mcpUrl, key)}
              onVerified={() => {
                setAddingMcp(false);
                setMcpName("");
                setMcpUrl("");
                void refresh();
              }}
            >
              <label className="conn-field">
                <span className="label">Name</span>
                <input
                  value={mcpName}
                  onChange={(e) => setMcpName(e.target.value)}
                  placeholder="my calendar"
                />
              </label>
              <label className="conn-field">
                <span className="label">Address</span>
                <input
                  value={mcpUrl}
                  onChange={(e) => setMcpUrl(e.target.value)}
                  placeholder="https://mcp.example.com"
                  spellCheck={false}
                />
              </label>
            </CaptureCard>
            <button className="btn-quiet" onClick={() => setAddingMcp(false)}>
              Not now
            </button>
          </div>
        )}
      </section>
    </div>
  );
}

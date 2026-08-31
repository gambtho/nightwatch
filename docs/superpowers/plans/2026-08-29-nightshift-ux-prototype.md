> Historical (2026-08-31): the prototype this plan built is removed from
> `main` (git history and the demo branches keep it). The real frontend is
> [`web/`](../../../web/README.md).

# Nightshift UX Prototype Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a clickable, fixture-driven prototype of Nightshift's four surfaces so we can put it in front of non-technical users and find out whether the design works.

**Architecture:** A single-page React app with no backend, no network calls, and no persistence. A screen enum in `App` drives navigation; a demo nav strip lets a facilitator jump to any surface during a session. All domain logic lives in pure, unit-tested functions under `src/lib/`; all content lives in `src/fixtures/`. The `PermitDiagram` component is shared by the Build and Approve screens — it is the same component in both, which is what makes the "watch your reach grow, then approve exactly that" story hold together.

**Tech Stack:** Vite, React 18, TypeScript, Vitest, @testing-library/react, jsdom, Prettier. Plain CSS with custom properties — no CSS framework.

**Spec:** `docs/superpowers/specs/2026-08-28-nightshift-design.md`

## Global Constraints

- **This is throwaway code.** Its output is a validated design, not a codebase we keep. Do not add abstraction for reuse that does not exist yet.
- **No network calls of any kind.** No `fetch`, no SDK, no Managed Agents API. Every value comes from `src/fixtures/`.
- **No persistence.** No `localStorage`, no backend. Reloading resets the demo — this is intentional and makes user-testing sessions repeatable.
- **Copy is part of the design, not filler.** Where this plan gives exact user-facing strings, use them verbatim. They were chosen and approved during design review.
- **The verdict must always name a limitation.** A `Verdict` with an empty `cannot` array is invalid. This is a product invariant with a test guarding it (Task 4).
- **Auto-pause fires at 3 consecutive failures of the same rule.** Not 2, not "some". Encoded in `shouldAutoPause` (Task 7).
- **Never promise an exact run time in UI copy.** The real substrate jitters scheduled runs by up to 15% of the interval. Use "Mondays at 9:00 AM" as a label, never "in 4 minutes".
- **Formatting:** all code blocks in this plan are written to Prettier 3 defaults (2-space indent, double quotes, semicolons, trailing commas). Task 1 pins `.prettierrc`. Run `npm run format` before every commit.
- **Working directory:** `/home/tng/workspace/nightshift-worktrees/prototype` on branch `nightshift-prototype`.

---

## File Structure

| File | Responsibility |
|---|---|
| `src/lib/types.ts` | Every domain type. No logic. |
| `src/lib/permit.ts` | Pure permit algebra — granting, querying, counting. |
| `src/lib/grading.ts` | Rule-failure detection and the auto-pause rule. |
| `src/fixtures/workflows.ts` | The support-digest workflow plus two more for the quiet home. |
| `src/fixtures/conversation.ts` | The scripted build conversation and its capability grants. |
| `src/fixtures/verdict.ts` | The feasibility verdict for the support-digest intake. |
| `src/components/PermitDiagram.tsx` | The blast radius. Shared by Build and Approve. |
| `src/components/Chat.tsx` | Message list plus the advance control. |
| `src/components/WorkflowCard.tsx` | One row on the quiet home. |
| `src/screens/Intake.tsx` | "What do you want taken care of?" |
| `src/screens/Verdict.tsx` | Can / would-get-wrong / needs-access. |
| `src/screens/Build.tsx` | Chat left, live permit right. |
| `src/screens/Approve.tsx` | Blast radius as the single gate. |
| `src/screens/Home.tsx` | The quiet state. |
| `src/screens/Alert.tsx` | The one interruption. |
| `src/App.tsx` | Screen machine plus demo nav. |
| `src/styles/tokens.css` | Colors, spacing, type scale. |

---

### Task 1: Scaffold, test harness, and design tokens

**Files:**
- Create: `package.json`, `vite.config.ts`, `tsconfig.json`, `.prettierrc`, `index.html`
- Create: `src/main.tsx`, `src/App.tsx`, `src/setupTests.ts`, `src/styles/tokens.css`
- Test: `src/App.test.tsx`

**Interfaces:**
- Consumes: nothing.
- Produces: a working `npm test` and `npm run dev`; the CSS custom properties every later task styles against.

- [ ] **Step 1: Create the package manifest**

```json
{
  "name": "nightshift-prototype",
  "private": true,
  "version": "0.0.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "test": "vitest run",
    "test:watch": "vitest",
    "format": "prettier --write \"src/**/*.{ts,tsx,css}\""
  },
  "dependencies": {
    "react": "^18.3.1",
    "react-dom": "^18.3.1"
  },
  "devDependencies": {
    "@testing-library/jest-dom": "^6.4.8",
    "@testing-library/react": "^16.0.1",
    "@testing-library/user-event": "^14.5.2",
    "@types/react": "^18.3.5",
    "@types/react-dom": "^18.3.0",
    "@vitejs/plugin-react": "^4.3.1",
    "jsdom": "^25.0.0",
    "prettier": "^3.3.3",
    "typescript": "^5.5.4",
    "vite": "^5.4.2",
    "vitest": "^2.0.5"
  }
}
```

- [ ] **Step 2: Create the config files**

`vite.config.ts`:

```typescript
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: "./src/setupTests.ts",
  },
});
```

`tsconfig.json`:

```json
{
  "compilerOptions": {
    "target": "ES2020",
    "useDefineForClassFields": true,
    "lib": ["ES2020", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "skipLibCheck": true,
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "resolveJsonModule": true,
    "isolatedModules": true,
    "noEmit": true,
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noFallthroughCasesInSwitch": true,
    "types": ["vitest/globals", "@testing-library/jest-dom"]
  },
  "include": ["src"]
}
```

`.prettierrc`:

```json
{
  "semi": true,
  "singleQuote": false,
  "trailingComma": "all",
  "printWidth": 90
}
```

`index.html`:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>Nightshift</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

- [ ] **Step 3: Create the test setup and design tokens**

`src/setupTests.ts`:

```typescript
import "@testing-library/jest-dom/vitest";
```

`src/styles/tokens.css`:

```css
:root {
  --bg: #0f1117;
  --surface: #171a23;
  --surface-2: #1e222d;
  --border: #2c3140;
  --text: #e6e8ee;
  --text-dim: #9aa1b1;
  --read: #16a34a;
  --write: #d97706;
  --limit: #dc2626;
  --accent: #6366f1;
  --radius: 10px;
  --gap: 12px;
  --font: ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif;
}

* {
  box-sizing: border-box;
}

body {
  margin: 0;
  background: var(--bg);
  color: var(--text);
  font-family: var(--font);
  line-height: 1.5;
}

.label {
  font-size: 0.65rem;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--text-dim);
}

.dim {
  color: var(--text-dim);
}
```

- [ ] **Step 4: Write the failing test**

`src/App.test.tsx`:

```typescript
import { render, screen } from "@testing-library/react";
import App from "./App";

test("renders the intake question on first load", () => {
  render(<App />);
  expect(screen.getByText("What do you want taken care of?")).toBeInTheDocument();
});
```

- [ ] **Step 5: Run test to verify it fails**

Run: `npm install && npm test`
Expected: FAIL — cannot resolve `./App`.

- [ ] **Step 6: Write minimal implementation**

`src/App.tsx`:

```typescript
export default function App() {
  return <h1>What do you want taken care of?</h1>;
}
```

`src/main.tsx`:

```typescript
import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import "./styles/tokens.css";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
```

- [ ] **Step 7: Run test to verify it passes**

Run: `npm test`
Expected: PASS — 1 test.

- [ ] **Step 8: Commit**

```bash
npm run format
git add -A
git commit -m "chore: scaffold Vite + React + Vitest prototype with design tokens"
```

---

### Task 2: Domain types and permit algebra

**Files:**
- Create: `src/lib/types.ts`, `src/lib/permit.ts`
- Test: `src/lib/permit.test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces: every type used by later tasks, and `emptyPermit(maxCostCents: number): Permit`, `grant(permit: Permit, capability: Capability): Permit`, `reads(permit: Permit): Capability[]`, `writes(permit: Permit): Capability[]`, `permitCounts(permit: Permit): { reads: number; writes: number }`.

- [ ] **Step 1: Write the types**

`src/lib/types.ts`:

```typescript
export type SystemId = "zendesk" | "slack" | "gmail" | "gcal" | "drive";

export type Access = "read" | "write";

export interface Capability {
  id: string;
  system: SystemId;
  label: string;
  access: Access;
  detail?: string;
}

export interface Permit {
  capabilities: Capability[];
  denied: string[];
  maxCostCents: number;
}

export interface RubricRule {
  id: string;
  text: string;
}

export interface WorkflowStep {
  id: string;
  text: string;
}

export interface Schedule {
  label: string;
  timezone: string;
}

export type RunStatus = "ok" | "failed" | "paused";

export interface RuleResult {
  ruleId: string;
  passed: boolean;
}

export interface Run {
  id: string;
  at: string;
  status: RunStatus;
  costCents: number;
  ruleResults: RuleResult[];
  summary: string;
}

export interface Workflow {
  id: string;
  name: string;
  schedule: Schedule;
  steps: WorkflowStep[];
  permit: Permit;
  rubric: RubricRule[];
  runs: Run[];
  paused: boolean;
}

export interface Verdict {
  can: string[];
  cannot: string[];
  access: Capability[];
}
```

- [ ] **Step 2: Write the failing test**

`src/lib/permit.test.ts`:

```typescript
import { emptyPermit, grant, permitCounts, reads, writes } from "./permit";
import type { Capability } from "./types";

const supportRead: Capability = {
  id: "slack-support-read",
  system: "slack",
  label: "Slack #support",
  access: "read",
  detail: "last 7 days",
};

const digestWrite: Capability = {
  id: "slack-digest-write",
  system: "slack",
  label: "Slack #team-digest",
  access: "write",
  detail: "post only",
};

test("an empty permit has no capabilities but keeps its spend cap", () => {
  const permit = emptyPermit(200);
  expect(permit.capabilities).toEqual([]);
  expect(permit.maxCostCents).toBe(200);
});

test("granting adds a capability without mutating the original permit", () => {
  const before = emptyPermit(200);
  const after = grant(before, supportRead);
  expect(before.capabilities).toHaveLength(0);
  expect(after.capabilities).toHaveLength(1);
});

test("granting the same capability twice does not duplicate it", () => {
  const permit = grant(grant(emptyPermit(200), supportRead), supportRead);
  expect(permit.capabilities).toHaveLength(1);
});

test("reads and writes are separated by access", () => {
  const permit = grant(grant(emptyPermit(200), supportRead), digestWrite);
  expect(reads(permit).map((c) => c.id)).toEqual(["slack-support-read"]);
  expect(writes(permit).map((c) => c.id)).toEqual(["slack-digest-write"]);
});

test("permitCounts summarizes both sides", () => {
  const permit = grant(grant(emptyPermit(200), supportRead), digestWrite);
  expect(permitCounts(permit)).toEqual({ reads: 1, writes: 1 });
});
```

- [ ] **Step 3: Run test to verify it fails**

Run: `npm test src/lib/permit.test.ts`
Expected: FAIL — cannot resolve `./permit`.

- [ ] **Step 4: Write minimal implementation**

`src/lib/permit.ts`:

```typescript
import type { Capability, Permit } from "./types";

export const DENIED_BY_DEFAULT = [
  "Email",
  "Direct messages",
  "Deleting anything",
  "Payments",
  "The rest of the internet",
];

export function emptyPermit(maxCostCents: number): Permit {
  return { capabilities: [], denied: DENIED_BY_DEFAULT, maxCostCents };
}

export function grant(permit: Permit, capability: Capability): Permit {
  if (permit.capabilities.some((c) => c.id === capability.id)) {
    return permit;
  }
  return { ...permit, capabilities: [...permit.capabilities, capability] };
}

export function reads(permit: Permit): Capability[] {
  return permit.capabilities.filter((c) => c.access === "read");
}

export function writes(permit: Permit): Capability[] {
  return permit.capabilities.filter((c) => c.access === "write");
}

export function permitCounts(permit: Permit): { reads: number; writes: number } {
  return { reads: reads(permit).length, writes: writes(permit).length };
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `npm test src/lib/permit.test.ts`
Expected: PASS — 5 tests.

- [ ] **Step 6: Commit**

```bash
npm run format
git add -A
git commit -m "feat: domain types and pure permit algebra"
```

---

### Task 3: PermitDiagram — the blast radius

**Files:**
- Create: `src/components/PermitDiagram.tsx`, `src/components/PermitDiagram.css`
- Test: `src/components/PermitDiagram.test.tsx`

**Interfaces:**
- Consumes: `Permit`, `reads`, `writes` from Task 2.
- Produces: `<PermitDiagram permit={permit} highlightIds={string[]} maxCostLabel={string} />`. `highlightIds` marks capabilities as newly added — the Build screen passes the current turn's grants; the Approve screen passes `[]`.

- [ ] **Step 1: Write the failing test**

`src/components/PermitDiagram.test.tsx`:

```typescript
import { render, screen } from "@testing-library/react";
import PermitDiagram from "./PermitDiagram";
import { emptyPermit, grant } from "../lib/permit";
import type { Capability } from "../lib/types";

const supportRead: Capability = {
  id: "slack-support-read",
  system: "slack",
  label: "Slack #support",
  access: "read",
  detail: "last 7 days",
};

const digestWrite: Capability = {
  id: "slack-digest-write",
  system: "slack",
  label: "Slack #team-digest",
  access: "write",
  detail: "post only",
};

const permit = grant(grant(emptyPermit(200), supportRead), digestWrite);

test("shows reads and writes with their detail text", () => {
  render(<PermitDiagram permit={permit} highlightIds={[]} maxCostLabel="$2.00 / run" />);
  expect(screen.getByText("Slack #support")).toBeInTheDocument();
  expect(screen.getByText("last 7 days")).toBeInTheDocument();
  expect(screen.getByText("Slack #team-digest")).toBeInTheDocument();
});

test("states the hard limit and the spend cap", () => {
  render(<PermitDiagram permit={permit} highlightIds={[]} maxCostLabel="$2.00 / run" />);
  expect(screen.getByText("CANNOT GO BEYOND THIS LINE")).toBeInTheDocument();
  expect(screen.getByText("$2.00 / run")).toBeInTheDocument();
});

test("lists what it can never do", () => {
  render(<PermitDiagram permit={permit} highlightIds={[]} maxCostLabel="$2.00 / run" />);
  expect(screen.getByText("Email")).toBeInTheDocument();
  expect(screen.getByText("Payments")).toBeInTheDocument();
});

test("marks highlighted capabilities as just added", () => {
  render(
    <PermitDiagram
      permit={permit}
      highlightIds={["slack-digest-write"]}
      maxCostLabel="$2.00 / run"
    />,
  );
  expect(screen.getByTestId("cap-slack-digest-write")).toHaveClass("just-added");
  expect(screen.getByTestId("cap-slack-support-read")).not.toHaveClass("just-added");
});

test("an empty permit shows an empty state on both sides", () => {
  render(
    <PermitDiagram
      permit={emptyPermit(200)}
      highlightIds={[]}
      maxCostLabel="$2.00 / run"
    />,
  );
  expect(screen.getAllByText("Nothing yet")).toHaveLength(2);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test src/components/PermitDiagram.test.tsx`
Expected: FAIL — cannot resolve `./PermitDiagram`.

- [ ] **Step 3: Write the implementation**

`src/components/PermitDiagram.tsx`:

```typescript
import type { Capability, Permit } from "../lib/types";
import { reads, writes } from "../lib/permit";
import "./PermitDiagram.css";

interface Props {
  permit: Permit;
  highlightIds: string[];
  maxCostLabel: string;
}

function CapabilityList({
  items,
  highlightIds,
}: {
  items: Capability[];
  highlightIds: string[];
}) {
  if (items.length === 0) {
    return <div className="cap-empty">Nothing yet</div>;
  }
  return (
    <>
      {items.map((c) => (
        <div
          key={c.id}
          data-testid={`cap-${c.id}`}
          className={`cap cap-${c.access}${highlightIds.includes(c.id) ? " just-added" : ""}`}
        >
          <div className="cap-label">{c.label}</div>
          {c.detail && <div className="cap-detail">{c.detail}</div>}
        </div>
      ))}
    </>
  );
}

export default function PermitDiagram({ permit, highlightIds, maxCostLabel }: Props) {
  const readItems = reads(permit);
  const writeItems = writes(permit);

  return (
    <div className="permit">
      <div className="permit-boundary">
        <span className="permit-boundary-tag">CANNOT GO BEYOND THIS LINE</span>
        <div className="permit-row">
          <div className="permit-col">
            <div className="label read-label">Can read</div>
            <CapabilityList items={readItems} highlightIds={highlightIds} />
          </div>

          <div className="permit-arrow">→</div>

          <div className="permit-agent">
            <div className="permit-agent-icon">🌙</div>
            <div className="permit-agent-name">the agent</div>
            <div className="permit-agent-cost">{maxCostLabel}</div>
          </div>

          <div className="permit-arrow">→</div>

          <div className="permit-col">
            <div className="label write-label">Can write</div>
            <CapabilityList items={writeItems} highlightIds={highlightIds} />
          </div>
        </div>
      </div>

      <div className="permit-denied">
        {permit.denied.map((d) => (
          <span key={d} className="denied-item">
            ✕ {d}
          </span>
        ))}
      </div>
    </div>
  );
}
```

`src/components/PermitDiagram.css`:

```css
.permit-boundary {
  border: 2px dashed var(--limit);
  border-radius: 14px;
  padding: 18px 14px 14px;
  position: relative;
}

.permit-boundary-tag {
  position: absolute;
  top: -10px;
  left: 14px;
  background: var(--limit);
  color: #fff;
  font-size: 0.6rem;
  letter-spacing: 0.05em;
  padding: 2px 8px;
  border-radius: 10px;
}

.permit-row {
  display: flex;
  align-items: center;
  gap: 10px;
}

.permit-col {
  flex: 1;
  min-width: 0;
}

.permit-arrow {
  opacity: 0.45;
}

.permit-agent {
  flex: 0.9;
  text-align: center;
  border: 2px solid var(--accent);
  background: rgb(99 102 241 / 0.15);
  border-radius: 12px;
  padding: 12px 8px;
}

.permit-agent-icon {
  font-size: 1.5rem;
}

.permit-agent-name {
  font-weight: 600;
  font-size: 0.85rem;
}

.permit-agent-cost {
  color: var(--text-dim);
  font-size: 0.72rem;
  margin-top: 4px;
}

.read-label {
  color: var(--read);
}

.write-label {
  color: var(--write);
}

.cap {
  border-radius: 8px;
  padding: 7px 9px;
  margin-bottom: 5px;
  font-size: 0.82rem;
}

.cap-read {
  background: rgb(22 163 74 / 0.12);
  border: 1px solid rgb(22 163 74 / 0.4);
}

.cap-write {
  background: rgb(217 119 6 / 0.12);
  border: 1px solid rgb(217 119 6 / 0.5);
}

.cap.just-added {
  box-shadow: 0 0 0 2px rgb(99 102 241 / 0.55);
}

.cap-detail {
  color: var(--text-dim);
  font-size: 0.75rem;
}

.cap-empty {
  color: var(--text-dim);
  font-size: 0.8rem;
  font-style: italic;
}

.permit-denied {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 14px;
  opacity: 0.45;
}

.denied-item {
  font-size: 0.75rem;
  text-decoration: line-through;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm test src/components/PermitDiagram.test.tsx`
Expected: PASS — 5 tests.

- [ ] **Step 5: Commit**

```bash
npm run format
git add -A
git commit -m "feat: PermitDiagram blast-radius component"
```

---

### Task 4: Intake and Verdict screens

**Files:**
- Create: `src/fixtures/verdict.ts`, `src/screens/Intake.tsx`, `src/screens/Verdict.tsx`, `src/screens/screens.css`
- Test: `src/screens/Verdict.test.tsx`, `src/screens/Intake.test.tsx`

**Interfaces:**
- Consumes: `Verdict`, `Capability` from Task 2.
- Produces: `<Intake onSubmit={(text: string) => void} />` and `<Verdict verdict={Verdict} onBuild={() => void} />`; `supportDigestVerdict: Verdict` from fixtures.

- [ ] **Step 1: Write the fixture**

`src/fixtures/verdict.ts`:

```typescript
import type { Verdict } from "../lib/types";

export const supportDigestVerdict: Verdict = {
  can: [
    "Read last week's tickets every Monday morning",
    "Group them by what's actually causing them, not by ticket",
    "Call out anything that looks security-related, separately",
    "Have it waiting in #team-digest before your standup",
  ],
  cannot: [
    "I can't tell you which of these engineering should drop everything for. I can rank by how often it comes up and how angry people are — but that call needs to stay yours.",
  ],
  access: [
    {
      id: "zendesk-read",
      system: "zendesk",
      label: "Your tickets",
      access: "read",
      detail: "read only",
    },
    {
      id: "slack-digest-write",
      system: "slack",
      label: "One channel to post in",
      access: "write",
      detail: "post only",
    },
  ],
};
```

- [ ] **Step 2: Write the failing tests**

`src/screens/Verdict.test.tsx`:

```typescript
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import VerdictScreen from "./Verdict";
import { supportDigestVerdict } from "../fixtures/verdict";

test("shows all three blocks", () => {
  render(<VerdictScreen verdict={supportDigestVerdict} onBuild={() => {}} />);
  expect(screen.getByText("I CAN DO THIS")).toBeInTheDocument();
  expect(screen.getByText("I'D GET THIS WRONG")).toBeInTheDocument();
  expect(screen.getByText("I'D NEED ACCESS TO")).toBeInTheDocument();
});

test("the fixture verdict names at least one limitation", () => {
  expect(supportDigestVerdict.cannot.length).toBeGreaterThan(0);
});

test("build button reports the user's intent", async () => {
  const onBuild = vi.fn();
  render(<VerdictScreen verdict={supportDigestVerdict} onBuild={onBuild} />);
  await userEvent.click(screen.getByRole("button", { name: "Build this" }));
  expect(onBuild).toHaveBeenCalledOnce();
});
```

`src/screens/Intake.test.tsx`:

```typescript
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Intake from "./Intake";

test("asks the question in the user's language", () => {
  render(<Intake onSubmit={() => {}} />);
  expect(screen.getByText("What do you want taken care of?")).toBeInTheDocument();
  expect(
    screen.getByText("Describe it how you'd describe it to a coworker."),
  ).toBeInTheDocument();
});

test("submitting passes the typed text up", async () => {
  const onSubmit = vi.fn();
  render(<Intake onSubmit={onSubmit} />);
  await userEvent.type(screen.getByRole("textbox"), "Every Monday I dig through tickets");
  await userEvent.click(screen.getByRole("button", { name: "See what you'd do" }));
  expect(onSubmit).toHaveBeenCalledWith("Every Monday I dig through tickets");
});

test("does not submit empty input", async () => {
  const onSubmit = vi.fn();
  render(<Intake onSubmit={onSubmit} />);
  await userEvent.click(screen.getByRole("button", { name: "See what you'd do" }));
  expect(onSubmit).not.toHaveBeenCalled();
});
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `npm test src/screens`
Expected: FAIL — cannot resolve `./Intake` and `./Verdict`.

- [ ] **Step 4: Write the implementations**

`src/screens/Intake.tsx`:

```typescript
import { useState } from "react";
import "./screens.css";

const STARTERS = [
  "I keep forgetting to follow up on…",
  "Someone should be watching…",
  "Every month I have to put together…",
];

export default function Intake({ onSubmit }: { onSubmit: (text: string) => void }) {
  const [text, setText] = useState("");

  return (
    <div className="screen screen-narrow">
      <div className="intake-head">
        <div className="intake-moon">🌙</div>
        <h1>What do you want taken care of?</h1>
        <p className="dim">Describe it how you'd describe it to a coworker.</p>
      </div>

      <textarea
        className="intake-box"
        value={text}
        onChange={(e) => setText(e.target.value)}
        rows={4}
      />

      <button
        className="btn"
        onClick={() => {
          if (text.trim()) onSubmit(text);
        }}
      >
        See what you'd do
      </button>

      <div className="label starters-label">Or start from one of these</div>
      <div className="starters">
        {STARTERS.map((s) => (
          <button key={s} className="starter" onClick={() => setText(s)}>
            {s}
          </button>
        ))}
      </div>

      <p className="dim intake-foot">Nothing is connected yet. Nothing runs yet.</p>
    </div>
  );
}
```

`src/screens/Verdict.tsx`:

```typescript
import type { Verdict } from "../lib/types";
import "./screens.css";

interface Props {
  verdict: Verdict;
  onBuild: () => void;
}

export default function VerdictScreen({ verdict, onBuild }: Props) {
  return (
    <div className="screen screen-narrow">
      <h2>Yes — mostly. Here's what I'd actually do.</h2>
      <p className="dim">
        And one part I'd get wrong, so I'm not going to pretend otherwise.
      </p>

      <div className="block block-can">
        <div className="label">I CAN DO THIS</div>
        {verdict.can.map((c) => (
          <div key={c}>✓ {c}</div>
        ))}
      </div>

      <div className="block block-cannot">
        <div className="label">I'D GET THIS WRONG</div>
        {verdict.cannot.map((c) => (
          <div key={c}>{c}</div>
        ))}
      </div>

      <div className="block block-access">
        <div className="label">I'D NEED ACCESS TO</div>
        {verdict.access.map((a) => (
          <div key={a.id}>
            {a.access === "read" ? "📖" : "✍️"} {a.label} — <em>{a.detail}</em>
          </div>
        ))}
        <p className="dim">You'll see exactly what it can touch before anything runs.</p>
      </div>

      <button className="btn" onClick={onBuild}>
        Build this
      </button>
    </div>
  );
}
```

`src/screens/screens.css`:

```css
.screen {
  padding: 28px 24px;
  max-width: 1040px;
  margin: 0 auto;
}

.screen-narrow {
  max-width: 620px;
}

.intake-head {
  text-align: center;
  margin-bottom: 18px;
}

.intake-moon {
  font-size: 2rem;
}

.intake-box {
  width: 100%;
  background: rgb(99 102 241 / 0.06);
  border: 2px solid rgb(99 102 241 / 0.45);
  border-radius: 12px;
  color: var(--text);
  font-family: var(--font);
  font-size: 0.95rem;
  padding: 12px;
  resize: vertical;
}

.starters-label {
  margin-top: 18px;
}

.starters {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 6px;
}

.starter,
.btn {
  font-family: var(--font);
  cursor: pointer;
  color: var(--text);
}

.starter {
  border: 1px solid var(--border);
  background: transparent;
  border-radius: 20px;
  padding: 5px 12px;
  font-size: 0.8rem;
}

.btn {
  margin-top: 14px;
  background: var(--accent);
  border: none;
  border-radius: 8px;
  padding: 9px 16px;
  font-size: 0.9rem;
}

.btn-secondary {
  background: var(--surface-2);
  border: 1px solid var(--border);
  margin-left: 8px;
}

.intake-foot {
  text-align: center;
  margin-top: 20px;
  font-size: 0.8rem;
}

.block {
  border-left: 3px solid var(--border);
  padding: 8px 0 8px 12px;
  margin: 14px 0;
}

.block-can {
  border-left-color: var(--read);
}

.block-cannot {
  border-left-color: var(--write);
  background: rgb(217 119 6 / 0.06);
}

.block-access {
  border-left-color: var(--accent);
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `npm test src/screens`
Expected: PASS — 6 tests.

- [ ] **Step 6: Commit**

```bash
npm run format
git add -A
git commit -m "feat: intake and honest-verdict screens"
```

---

### Task 5: Build screen with live permit

**Files:**
- Create: `src/fixtures/conversation.ts`, `src/components/Chat.tsx`, `src/screens/Build.tsx`
- Test: `src/fixtures/conversation.test.ts`, `src/screens/Build.test.tsx`

**Interfaces:**
- Consumes: `PermitDiagram` (Task 3), `grant`/`emptyPermit` (Task 2).
- Produces: `BuildTurn` type, `buildScript: BuildTurn[]`, `permitAfter(turns: BuildTurn[], upTo: number): Permit`, `<Build onApprove={() => void} />`.

- [ ] **Step 1: Write the failing test for the conversation logic**

`src/fixtures/conversation.test.ts`:

```typescript
import { buildScript, permitAfter } from "./conversation";

test("the permit starts empty", () => {
  expect(permitAfter(buildScript, 0).capabilities).toHaveLength(0);
});

test("the permit accumulates grants as turns advance", () => {
  const full = permitAfter(buildScript, buildScript.length);
  expect(full.capabilities.length).toBeGreaterThan(1);
});

test("permitAfter is monotonic — reach never shrinks", () => {
  let previous = 0;
  for (let i = 0; i <= buildScript.length; i++) {
    const count = permitAfter(buildScript, i).capabilities.length;
    expect(count).toBeGreaterThanOrEqual(previous);
    previous = count;
  }
});

test("the script ends with at least one read and one write", () => {
  const full = permitAfter(buildScript, buildScript.length);
  expect(full.capabilities.some((c) => c.access === "read")).toBe(true);
  expect(full.capabilities.some((c) => c.access === "write")).toBe(true);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test src/fixtures/conversation.test.ts`
Expected: FAIL — cannot resolve `./conversation`.

- [ ] **Step 3: Write the conversation fixture**

`src/fixtures/conversation.ts`:

```typescript
import { emptyPermit, grant } from "../lib/permit";
import type { Capability, Permit } from "../lib/types";

export interface BuildTurn {
  id: string;
  speaker: "user" | "nightshift";
  text: string;
  grants: Capability[];
}

export const MAX_COST_CENTS = 200;

export const buildScript: BuildTurn[] = [
  {
    id: "t1",
    speaker: "user",
    text: "Every Monday, look at last week's support tickets and tell the team what keeps coming up.",
    grants: [],
  },
  {
    id: "t2",
    speaker: "nightshift",
    text: "Got it. I'll need to read your tickets — is that Zendesk, or the #support Slack channel?",
    grants: [
      {
        id: "zendesk-read",
        system: "zendesk",
        label: "Zendesk tickets",
        access: "read",
        detail: "read only",
      },
    ],
  },
  {
    id: "t3",
    speaker: "user",
    text: "Both. Post the summary in #team-digest.",
    grants: [
      {
        id: "slack-support-read",
        system: "slack",
        label: "Slack #support",
        access: "read",
        detail: "last 7 days",
      },
      {
        id: "slack-digest-write",
        system: "slack",
        label: "Slack #team-digest",
        access: "write",
        detail: "post only",
      },
    ],
  },
  {
    id: "t4",
    speaker: "nightshift",
    text: "Done. What should I do if something looks like a security problem?",
    grants: [],
  },
  {
    id: "t5",
    speaker: "user",
    text: "Flag it separately at the top, and don't bury it.",
    grants: [],
  },
  {
    id: "t6",
    speaker: "nightshift",
    text: "That's everything I need. Want to see exactly what this will be able to touch?",
    grants: [],
  },
];

export function permitAfter(turns: BuildTurn[], upTo: number): Permit {
  return turns
    .slice(0, upTo)
    .flatMap((t) => t.grants)
    .reduce(grant, emptyPermit(MAX_COST_CENTS));
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm test src/fixtures/conversation.test.ts`
Expected: PASS — 4 tests.

- [ ] **Step 5: Write the failing test for the Build screen**

`src/screens/Build.test.tsx`:

```typescript
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Build from "./Build";

test("starts with only the first turn and an empty permit", () => {
  render(<Build onApprove={() => {}} />);
  expect(
    screen.getByText(/Every Monday, look at last week's support tickets/),
  ).toBeInTheDocument();
  expect(screen.getAllByText("Nothing yet")).toHaveLength(2);
});

test("advancing the conversation grows the permit", async () => {
  render(<Build onApprove={() => {}} />);
  const next = screen.getByRole("button", { name: "Continue" });
  await userEvent.click(next);
  expect(screen.getByText("Zendesk tickets")).toBeInTheDocument();
  await userEvent.click(next);
  expect(screen.getByText("Slack #team-digest")).toBeInTheDocument();
});

test("the approve action appears only at the end of the script", async () => {
  render(<Build onApprove={() => {}} />);
  expect(screen.queryByRole("button", { name: "Review what it can touch" })).toBeNull();
  const next = screen.getByRole("button", { name: "Continue" });
  for (let i = 0; i < 5; i++) {
    await userEvent.click(next);
  }
  expect(
    screen.getByRole("button", { name: "Review what it can touch" }),
  ).toBeInTheDocument();
});
```

- [ ] **Step 6: Run test to verify it fails**

Run: `npm test src/screens/Build.test.tsx`
Expected: FAIL — cannot resolve `./Build`.

- [ ] **Step 7: Write the implementations**

`src/components/Chat.tsx`:

```typescript
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
```

`src/screens/Build.tsx`:

```typescript
import { useState } from "react";
import Chat from "../components/Chat";
import PermitDiagram from "../components/PermitDiagram";
import { buildScript, permitAfter } from "../fixtures/conversation";
import "./screens.css";

export default function Build({ onApprove }: { onApprove: () => void }) {
  const [shown, setShown] = useState(1);
  const permit = permitAfter(buildScript, shown);
  const highlightIds = buildScript[shown - 1].grants.map((g) => g.id);
  const atEnd = shown >= buildScript.length;

  return (
    <div className="screen build">
      <div className="build-chat">
        <div className="label">Chat</div>
        <Chat turns={buildScript.slice(0, shown)} />
        {atEnd ? (
          <button className="btn" onClick={onApprove}>
            Review what it can touch
          </button>
        ) : (
          <button className="btn" onClick={() => setShown(shown + 1)}>
            Continue
          </button>
        )}
      </div>

      <div className="build-permit">
        <div className="label">Its reach — updating live</div>
        <PermitDiagram
          permit={permit}
          highlightIds={highlightIds}
          maxCostLabel="max $2.00 / run"
        />
      </div>
    </div>
  );
}
```

Append to `src/screens/screens.css`:

```css
.build {
  display: grid;
  grid-template-columns: 1.15fr 1fr;
  gap: 22px;
  align-items: start;
}

.chat {
  margin: 10px 0 14px;
}

.bubble {
  border-radius: 12px;
  padding: 9px 12px;
  margin-bottom: 10px;
  font-size: 0.87rem;
  max-width: 88%;
}

.bubble-user {
  background: rgb(99 102 241 / 0.16);
  margin-left: auto;
  border-bottom-right-radius: 4px;
}

.bubble-nightshift {
  background: var(--surface-2);
  border-bottom-left-radius: 4px;
}
```

- [ ] **Step 8: Run tests to verify they pass**

Run: `npm test`
Expected: PASS — all suites green.

- [ ] **Step 9: Commit**

```bash
npm run format
git add -A
git commit -m "feat: build screen with live-updating permit"
```

---

### Task 6: Approve screen

**Files:**
- Create: `src/screens/Approve.tsx`
- Test: `src/screens/Approve.test.tsx`

**Interfaces:**
- Consumes: `PermitDiagram` (Task 3), `buildScript`/`permitAfter` (Task 5).
- Produces: `<Approve onApproved={() => void} />`.

- [ ] **Step 1: Write the failing test**

`src/screens/Approve.test.tsx`:

```typescript
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Approve from "./Approve";

test("shows the workflow name and its schedule label", () => {
  render(<Approve onApproved={() => {}} />);
  expect(screen.getByText("Weekly support digest")).toBeInTheDocument();
  expect(screen.getByText(/Mondays at 9:00 AM/)).toBeInTheDocument();
});

test("shows the full permit, not a partial one", () => {
  render(<Approve onApproved={() => {}} />);
  expect(screen.getByText("Zendesk tickets")).toBeInTheDocument();
  expect(screen.getByText("Slack #support")).toBeInTheDocument();
  expect(screen.getByText("Slack #team-digest")).toBeInTheDocument();
});

test("nothing is highlighted as newly added on the approval screen", () => {
  render(<Approve onApproved={() => {}} />);
  expect(screen.getByTestId("cap-zendesk-read")).not.toHaveClass("just-added");
});

test("approving reports up", async () => {
  const onApproved = vi.fn();
  render(<Approve onApproved={onApproved} />);
  await userEvent.click(screen.getByRole("button", { name: "Approve & schedule" }));
  expect(onApproved).toHaveBeenCalledOnce();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test src/screens/Approve.test.tsx`
Expected: FAIL — cannot resolve `./Approve`.

- [ ] **Step 3: Write the implementation**

`src/screens/Approve.tsx`:

```typescript
import PermitDiagram from "../components/PermitDiagram";
import { buildScript, permitAfter } from "../fixtures/conversation";
import "./screens.css";

export default function Approve({ onApproved }: { onApproved: () => void }) {
  const permit = permitAfter(buildScript, buildScript.length);

  return (
    <div className="screen screen-narrow">
      <h2>Weekly support digest</h2>
      <p className="dim">Runs Mondays at 9:00 AM · America/New_York</p>

      <PermitDiagram permit={permit} highlightIds={[]} maxCostLabel="max $2.00 / run" />

      <p className="dim approve-note">
        It stops after 2 bad runs and tells you. It never retries silently.
      </p>

      <button className="btn" onClick={onApproved}>
        Approve &amp; schedule
      </button>
      <button className="btn btn-secondary">Change what it can reach</button>
    </div>
  );
}
```

Append to `src/screens/screens.css`:

```css
.approve-note {
  font-size: 0.83rem;
  margin-top: 16px;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm test src/screens/Approve.test.tsx`
Expected: PASS — 4 tests.

- [ ] **Step 5: Commit**

```bash
npm run format
git add -A
git commit -m "feat: approval screen reusing the permit diagram"
```

---

### Task 7: Grading logic, quiet home, and the alert

**Files:**
- Create: `src/lib/grading.ts`, `src/fixtures/workflows.ts`, `src/components/WorkflowCard.tsx`, `src/screens/Home.tsx`, `src/screens/Alert.tsx`
- Test: `src/lib/grading.test.ts`, `src/screens/Home.test.tsx`, `src/screens/Alert.test.tsx`

**Interfaces:**
- Consumes: `Workflow`, `RubricRule`, `Run` (Task 2).
- Produces: `consecutiveFailures(workflow: Workflow, ruleId: string): number`, `failingRules(workflow: Workflow): RubricRule[]`, `shouldAutoPause(workflow: Workflow): boolean`, `supportDigest`/`renewals`/`unanswered: Workflow`, `<Home onNew={() => void} />`, `<Alert />`.

- [ ] **Step 1: Write the failing test for grading**

`src/lib/grading.test.ts`:

```typescript
import { consecutiveFailures, failingRules, shouldAutoPause } from "./grading";
import type { Run, Workflow } from "./types";

function run(id: string, securityPassed: boolean): Run {
  return {
    id,
    at: `2026-08-${id}T09:00:00Z`,
    status: "ok",
    costCents: 41,
    ruleResults: [
      { ruleId: "themes", passed: true },
      { ruleId: "security", passed: securityPassed },
    ],
    summary: "Posted to #team-digest",
  };
}

function workflowWith(runs: Run[]): Workflow {
  return {
    id: "wf",
    name: "Weekly support digest",
    schedule: { label: "Mondays at 9:00 AM", timezone: "America/New_York" },
    steps: [],
    permit: { capabilities: [], denied: [], maxCostCents: 200 },
    rubric: [
      { id: "themes", text: "Groups complaints by theme, not by ticket" },
      { id: "security", text: "Flags anything security-related separately" },
    ],
    runs,
    paused: false,
  };
}

test("counts consecutive failures from the most recent run backwards", () => {
  const wf = workflowWith([run("10", true), run("17", false), run("24", false)]);
  expect(consecutiveFailures(wf, "security")).toBe(2);
  expect(consecutiveFailures(wf, "themes")).toBe(0);
});

test("a passing run resets the streak", () => {
  const wf = workflowWith([run("10", false), run("17", false), run("24", true)]);
  expect(consecutiveFailures(wf, "security")).toBe(0);
});

test("failingRules reports only rules currently failing", () => {
  const wf = workflowWith([run("17", false), run("24", false)]);
  expect(failingRules(wf).map((r) => r.id)).toEqual(["security"]);
});

test("auto-pause does not fire at two consecutive failures", () => {
  const wf = workflowWith([run("17", false), run("24", false)]);
  expect(shouldAutoPause(wf)).toBe(false);
});

test("auto-pause fires at three consecutive failures", () => {
  const wf = workflowWith([run("10", false), run("17", false), run("24", false)]);
  expect(shouldAutoPause(wf)).toBe(true);
});

test("a workflow with no runs is not paused", () => {
  expect(shouldAutoPause(workflowWith([]))).toBe(false);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test src/lib/grading.test.ts`
Expected: FAIL — cannot resolve `./grading`.

- [ ] **Step 3: Write the implementation**

`src/lib/grading.ts`:

```typescript
import type { RubricRule, Workflow } from "./types";

export const AUTO_PAUSE_THRESHOLD = 3;

export function consecutiveFailures(workflow: Workflow, ruleId: string): number {
  let streak = 0;
  for (let i = workflow.runs.length - 1; i >= 0; i--) {
    const result = workflow.runs[i].ruleResults.find((r) => r.ruleId === ruleId);
    if (!result || result.passed) break;
    streak++;
  }
  return streak;
}

export function failingRules(workflow: Workflow): RubricRule[] {
  return workflow.rubric.filter((rule) => consecutiveFailures(workflow, rule.id) > 0);
}

export function shouldAutoPause(workflow: Workflow): boolean {
  return workflow.rubric.some(
    (rule) => consecutiveFailures(workflow, rule.id) >= AUTO_PAUSE_THRESHOLD,
  );
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm test src/lib/grading.test.ts`
Expected: PASS — 6 tests.

- [ ] **Step 5: Write the workflow fixtures**

`src/fixtures/workflows.ts`:

```typescript
import type { Workflow } from "../lib/types";

export const supportDigest: Workflow = {
  id: "wf-digest",
  name: "Weekly support digest",
  schedule: { label: "Mondays at 9:00 AM", timezone: "America/New_York" },
  steps: [{ id: "s1", text: "Summarize recurring support themes" }],
  permit: { capabilities: [], denied: [], maxCostCents: 200 },
  rubric: [
    { id: "themes", text: "Groups complaints by theme, not by ticket" },
    { id: "security", text: "Flags anything security-related separately" },
    { id: "length", text: "Fits in one screen" },
  ],
  runs: [
    {
      id: "r1",
      at: "2026-08-24T09:00:00Z",
      status: "ok",
      costCents: 41,
      ruleResults: [
        { ruleId: "themes", passed: true },
        { ruleId: "security", passed: true },
        { ruleId: "length", passed: true },
      ],
      summary: "Posted to #team-digest · met all 3 of your rules",
    },
  ],
  paused: false,
};

export const renewals: Workflow = {
  id: "wf-renewals",
  name: "Contract renewals coming up",
  schedule: { label: "Every morning at 7:00 AM", timezone: "America/New_York" },
  steps: [{ id: "s1", text: "Check for contracts due in 60 days" }],
  permit: { capabilities: [], denied: [], maxCostCents: 100 },
  rubric: [{ id: "window", text: "Looks 60 days ahead" }],
  runs: [
    {
      id: "r1",
      at: "2026-08-29T07:00:00Z",
      status: "ok",
      costCents: 12,
      ruleResults: [{ ruleId: "window", passed: true }],
      summary: "Nothing due in the next 60 days",
    },
  ],
  paused: false,
};

export const unanswered: Workflow = {
  id: "wf-unanswered",
  name: "Unanswered customer questions",
  schedule: { label: "Every day at 5:00 PM", timezone: "America/New_York" },
  steps: [{ id: "s1", text: "Nudge threads with no reply" }],
  permit: { capabilities: [], denied: [], maxCostCents: 100 },
  rubric: [
    { id: "age", text: "Only threads older than 24 hours" },
    { id: "tone", text: "Nudges politely" },
  ],
  runs: [
    {
      id: "r1",
      at: "2026-08-29T17:00:00Z",
      status: "ok",
      costCents: 8,
      ruleResults: [
        { ruleId: "age", passed: true },
        { ruleId: "tone", passed: true },
      ],
      summary: "Nudged 2 threads · met all 2 of your rules",
    },
  ],
  paused: false,
};

export const allWorkflows: Workflow[] = [supportDigest, renewals, unanswered];
```

- [ ] **Step 6: Write the failing tests for the screens**

`src/screens/Home.test.tsx`:

```typescript
import { render, screen } from "@testing-library/react";
import Home from "./Home";

test("lists every workflow with its last summary", () => {
  render(<Home onNew={() => {}} />);
  expect(screen.getByText("Weekly support digest")).toBeInTheDocument();
  expect(screen.getByText("Contract renewals coming up")).toBeInTheDocument();
  expect(screen.getByText("Unanswered customer questions")).toBeInTheDocument();
});

test("tells the user they do not need to be here", () => {
  render(<Home onNew={() => {}} />);
  expect(screen.getByText("You don't need to check this page.")).toBeInTheDocument();
  expect(
    screen.getByText("If something goes wrong, we'll come find you."),
  ).toBeInTheDocument();
});
```

`src/screens/Alert.test.tsx`:

```typescript
import { render, screen } from "@testing-library/react";
import Alert from "./Alert";

test("names the failing rule and how long it has failed", () => {
  render(<Alert />);
  expect(
    screen.getByText(/Flags anything security-related separately/),
  ).toBeInTheDocument();
  expect(screen.getByText(/Failed 3 Mondays running/)).toBeInTheDocument();
});

test("explains the suspected cause and what it already did", () => {
  render(<Alert />);
  expect(screen.getByText("WHY I THINK IT'S HAPPENING")).toBeInTheDocument();
  expect(screen.getByText(/Paused it/)).toBeInTheDocument();
});
```

- [ ] **Step 7: Run tests to verify they fail**

Run: `npm test src/screens/Home.test.tsx src/screens/Alert.test.tsx`
Expected: FAIL — cannot resolve `./Home` and `./Alert`.

- [ ] **Step 8: Write the implementations**

`src/components/WorkflowCard.tsx`:

```typescript
import type { Workflow } from "../lib/types";

function dollars(cents: number): string {
  return `$${(cents / 100).toFixed(2)}`;
}

export default function WorkflowCard({ workflow }: { workflow: Workflow }) {
  const last = workflow.runs[workflow.runs.length - 1];
  return (
    <div className="wf-card">
      <div className="wf-card-top">
        <strong>{workflow.name}</strong>
        <span className="wf-ok">✓ ran</span>
      </div>
      <div className="dim wf-card-line">
        {last.summary} · {dollars(last.costCents)}
      </div>
      <div className="dim wf-card-line">Next: {workflow.schedule.label}</div>
    </div>
  );
}
```

`src/screens/Home.tsx`:

```typescript
import WorkflowCard from "../components/WorkflowCard";
import { allWorkflows } from "../fixtures/workflows";
import "./screens.css";

export default function Home({ onNew }: { onNew: () => void }) {
  return (
    <div className="screen screen-narrow">
      <h2>🌙 Everything's running</h2>

      {allWorkflows.map((w) => (
        <WorkflowCard key={w.id} workflow={w} />
      ))}

      <div className="home-foot dim">
        <div>You don't need to check this page.</div>
        <div>If something goes wrong, we'll come find you.</div>
      </div>

      <button className="btn" onClick={onNew}>
        + Something else you want taken care of
      </button>
    </div>
  );
}
```

`src/screens/Alert.tsx`:

```typescript
import "./screens.css";

export default function Alert() {
  return (
    <div className="screen screen-narrow">
      <p className="dim alert-channel">
        Email + push — they didn't open the app. It found them.
      </p>

      <h2>⚠️ Your Monday digest has been getting worse, and I think I know why.</h2>
      <p className="dim">
        It still ran. It just stopped doing one of the things you asked for.
      </p>

      <div className="block block-cannot">
        <div className="label">THE RULE IT'S MISSING</div>
        <div>"Flags anything security-related separately"</div>
        <p className="dim">
          Failed 3 Mondays running. Your other 2 rules are still fine.
        </p>
      </div>

      <div className="block block-access">
        <div className="label">WHY I THINK IT'S HAPPENING</div>
        <div>
          Since Aug 4, almost every ticket has come through with an empty{" "}
          <strong>category</strong> field. I've been guessing which ones are security
          issues, and I've been guessing badly.
        </div>
      </div>

      <div className="block block-can">
        <div className="label">WHAT I DID ABOUT IT</div>
        <div>
          Paused it. It won't run again until you decide — I'd rather stop than keep
          sending you something you trust and shouldn't.
        </div>
      </div>

      <button className="btn">Show me the 3 runs</button>
      <button className="btn btn-secondary">Let's fix it</button>
      <button className="btn btn-secondary">It's fine, resume</button>
    </div>
  );
}
```

Append to `src/screens/screens.css`:

```css
.wf-card {
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 11px 13px;
  margin-bottom: 9px;
}

.wf-card-top {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
}

.wf-ok {
  color: var(--read);
  font-size: 0.78rem;
}

.wf-card-line {
  font-size: 0.79rem;
}

.home-foot {
  text-align: center;
  border-top: 1px solid var(--border);
  padding-top: 13px;
  margin-top: 16px;
  font-size: 0.83rem;
}

.alert-channel {
  background: rgb(217 119 6 / 0.1);
  border: 1px solid rgb(217 119 6 / 0.45);
  border-radius: 9px;
  padding: 8px 11px;
  font-size: 0.78rem;
}
```

- [ ] **Step 9: Run tests to verify they pass**

Run: `npm test`
Expected: PASS — all suites green.

- [ ] **Step 10: Commit**

```bash
npm run format
git add -A
git commit -m "feat: grading logic, quiet home, and the alert screen"
```

---

### Task 8: Wire the flow and the facilitator nav

**Files:**
- Modify: `src/App.tsx` (replace entirely), `src/App.test.tsx` (replace entirely)
- Create: `src/App.css`

**Interfaces:**
- Consumes: every screen from Tasks 4–7.
- Produces: the runnable demo.

- [ ] **Step 1: Write the failing test**

`src/App.test.tsx` (replace the Task 1 contents):

```typescript
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import App from "./App";

test("starts on intake", () => {
  render(<App />);
  expect(screen.getByText("What do you want taken care of?")).toBeInTheDocument();
});

test("walks intake to verdict to build", async () => {
  render(<App />);
  await userEvent.type(screen.getByRole("textbox"), "Every Monday I dig through tickets");
  await userEvent.click(screen.getByRole("button", { name: "See what you'd do" }));
  expect(screen.getByText("I'D GET THIS WRONG")).toBeInTheDocument();

  await userEvent.click(screen.getByRole("button", { name: "Build this" }));
  expect(screen.getByText("Its reach — updating live")).toBeInTheDocument();
});

test("the facilitator nav jumps straight to any screen", async () => {
  render(<App />);
  await userEvent.click(screen.getByRole("button", { name: "Alert" }));
  expect(screen.getByText("THE RULE IT'S MISSING")).toBeInTheDocument();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test src/App.test.tsx`
Expected: FAIL — no facilitator nav, no verdict transition.

- [ ] **Step 3: Write the implementation**

`src/App.tsx`:

```typescript
import { useState } from "react";
import Intake from "./screens/Intake";
import VerdictScreen from "./screens/Verdict";
import Build from "./screens/Build";
import Approve from "./screens/Approve";
import Home from "./screens/Home";
import Alert from "./screens/Alert";
import { supportDigestVerdict } from "./fixtures/verdict";
import "./App.css";

type Screen = "intake" | "verdict" | "build" | "approve" | "home" | "alert";

const NAV: { id: Screen; label: string }[] = [
  { id: "intake", label: "Intake" },
  { id: "verdict", label: "Verdict" },
  { id: "build", label: "Build" },
  { id: "approve", label: "Approve" },
  { id: "home", label: "Home" },
  { id: "alert", label: "Alert" },
];

export default function App() {
  const [screen, setScreen] = useState<Screen>("intake");

  return (
    <>
      <nav className="demo-nav">
        <span className="demo-nav-title">🌙 Nightshift prototype</span>
        {NAV.map((n) => (
          <button
            key={n.id}
            className={`demo-nav-btn${screen === n.id ? " active" : ""}`}
            onClick={() => setScreen(n.id)}
          >
            {n.label}
          </button>
        ))}
      </nav>

      {screen === "intake" && <Intake onSubmit={() => setScreen("verdict")} />}
      {screen === "verdict" && (
        <VerdictScreen
          verdict={supportDigestVerdict}
          onBuild={() => setScreen("build")}
        />
      )}
      {screen === "build" && <Build onApprove={() => setScreen("approve")} />}
      {screen === "approve" && <Approve onApproved={() => setScreen("home")} />}
      {screen === "home" && <Home onNew={() => setScreen("intake")} />}
      {screen === "alert" && <Alert />}
    </>
  );
}
```

`src/App.css`:

```css
.demo-nav {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 14px;
  background: var(--surface);
  border-bottom: 1px solid var(--border);
}

.demo-nav-title {
  font-size: 0.8rem;
  color: var(--text-dim);
  margin-right: 10px;
}

.demo-nav-btn {
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 20px;
  color: var(--text-dim);
  cursor: pointer;
  font-family: var(--font);
  font-size: 0.75rem;
  padding: 3px 11px;
}

.demo-nav-btn.active {
  background: var(--accent);
  border-color: var(--accent);
  color: #fff;
}
```

- [ ] **Step 4: Run the full suite**

Run: `npm test`
Expected: PASS — all suites green.

- [ ] **Step 5: Verify it actually runs in a browser**

Run: `npm run dev`
Expected: the intake screen loads; the nav jumps between all six screens; the Build screen's permit grows on each Continue.

- [ ] **Step 6: Commit**

```bash
npm run format
git add -A
git commit -m "feat: wire the demo flow with a facilitator nav"
```

---

## Self-Review

**Spec coverage.** Intake and verdict → Task 4. Build with live permit → Task 5. Blast-radius approval → Tasks 3 and 6. Quiet home and alert → Task 7. Three artifacts: steps and permit are modeled in `types.ts` (Task 2), rubric is modeled and *enforced* through `grading.ts` (Task 7). Auto-pause at 3 → Task 7. "Never promise an exact run time" → schedules are labels only. Prototype scope's "faked" list is honored: no network, no persistence, scripted conversation, fixture runs.

**Deliberate gaps, called out rather than hidden:**
- The spec's *steps* artifact has a type but no screen renders it. The four surfaces we designed don't show a step list, so building one would be inventing UI that never passed design review.
- `Workflow.permit` is empty in the fixtures. The Approve screen derives its permit from the conversation script instead, which is the path a real user walks. Wiring fixture permits would be unused code.
- The prototype's four test questions are answered by *running sessions*, not by code. No task claims to answer them.

**Placeholder scan:** no TBDs; every code step carries real content.

**Type consistency:** `Capability.access` is `"read" | "write"` throughout. `permitAfter(turns, upTo)` keeps its signature in Tasks 5 and 6. `highlightIds` is `string[]` in Tasks 3, 5, and 6. `consecutiveFailures(workflow, ruleId)` is consistent between definition and callers.

**One known ordering constraint:** Task 8 replaces `src/App.test.tsx` wholesale. Do not try to merge it with the Task 1 version.

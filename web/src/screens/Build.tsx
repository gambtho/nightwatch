import { Link } from "react-router-dom";
import "./screens.css";

// First login lands here (the server's firstLoginPath). The build
// conversation — describe the job, watch the permit grow — needs the
// server's build resource, which isn't live yet. This screen claims the
// route and says so plainly rather than faking the conversation.

export default function Build() {
  return (
    <div className="screen build">
      <h1>What do you want taken care of?</h1>
      <p className="dim">
        Soon, you'll describe it here the way you'd describe it to a coworker — "every
        Monday I spend an hour digging through tickets" — and Tomte will tell you honestly
        what it can do, what it would get wrong, and what it would need to reach.
      </p>
      <p className="dim">
        That conversation isn't ready yet. Workflows set up another way appear on{" "}
        <Link to="/">your home page</Link>, where each one waits for your approval before
        it can run.
      </p>
      <p className="dim">
        Building or demoing? There's a <Link to="/setup">developer setup</Link> form that
        writes the version-1 document by hand — it's the scaffolding, not the product.
      </p>
    </div>
  );
}

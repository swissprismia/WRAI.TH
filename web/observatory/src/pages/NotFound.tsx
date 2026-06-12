import { Link } from "react-router-dom";

export default function NotFound() {
  return (
    <>
      <header className="page-header">
        <h2>Not found</h2>
      </header>
      <div className="empty">
        The requested resource has no rows in the observatory.
        <div style={{ marginTop: "0.75rem" }}>
          <Link to="/">Back to overview</Link>
        </div>
      </div>
    </>
  );
}

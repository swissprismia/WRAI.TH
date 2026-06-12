import { NavLink, Outlet } from "react-router-dom";

const navClass = ({ isActive }: { isActive: boolean }) =>
  `nav-item${isActive ? " active" : ""}`;

export default function Layout() {
  return (
    <div className="app">
      <aside className="sidebar">
        <h1>Factory Observatory</h1>
        <NavLink to="/" end className={navClass}>
          Overview
        </NavLink>
        <NavLink to="/budgets" className={navClass}>
          Budgets
        </NavLink>
        <div className="nav-spacer" />
        <div className="nav-foot">
          ADF-083 · served by WRAI.TH
          <div style={{ marginTop: "0.4rem" }}>
            <a href="/chat/">CTO Chat →</a>
          </div>
        </div>
      </aside>
      <main className="main">
        <Outlet />
      </main>
    </div>
  );
}

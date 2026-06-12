import { BrowserRouter, Route, Routes } from "react-router-dom";

import Layout from "./components/Layout";
import AgentDetail from "./pages/AgentDetail";
import Budgets from "./pages/Budgets";
import NotFound from "./pages/NotFound";
import Overview from "./pages/Overview";
import ProjectDetail from "./pages/ProjectDetail";
import SessionDetail from "./pages/SessionDetail";
import TaskDetail from "./pages/TaskDetail";

export default function App() {
  return (
    <BrowserRouter basename="/observatory">
      <Routes>
        <Route element={<Layout />}>
          <Route index element={<Overview />} />
          <Route path="projects/:slug" element={<ProjectDetail />} />
          <Route path="agents/:slug" element={<AgentDetail />} />
          <Route path="sessions/:id" element={<SessionDetail />} />
          <Route path="tasks/:id" element={<TaskDetail />} />
          <Route path="budgets" element={<Budgets />} />
          <Route path="*" element={<NotFound />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}

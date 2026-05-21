import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";

import ChatPage from "./pages/ChatPage";
import ProjectList from "./pages/ProjectList";

export default function App() {
  return (
    <BrowserRouter basename="/chat">
      <Routes>
        <Route path="/" element={<ProjectList />} />
        <Route path="/p/:slug" element={<ChatPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </BrowserRouter>
  );
}

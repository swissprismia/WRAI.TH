import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";

import Layout from "./components/Layout";
import ChatPage from "./pages/ChatPage";
import EmptyChat from "./pages/EmptyChat";

export default function App() {
  return (
    <BrowserRouter basename="/chat">
      <Routes>
        <Route element={<Layout />}>
          <Route index element={<EmptyChat />} />
          <Route path="p/:slug" element={<ChatPage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}

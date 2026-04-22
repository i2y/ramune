import React from "react";
import { createRoot } from "react-dom/client";
import { LangProvider } from "./shared.jsx";
import { VariantB } from "./variant-b.jsx";

createRoot(document.getElementById("root")).render(
  <LangProvider>
    <VariantB />
  </LangProvider>
);

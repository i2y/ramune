import React from "react";
import { hydrateRoot } from "react-dom/client";
import { LangProvider } from "./shared.jsx";
import { VariantB } from "./variant-b.jsx";

hydrateRoot(
  document.getElementById("root"),
  <LangProvider>
    <VariantB />
  </LangProvider>
);

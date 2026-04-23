import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { LangProvider } from "./shared.jsx";
import { VariantB } from "./variant-b.jsx";

export function render() {
  return renderToStaticMarkup(
    <LangProvider>
      <VariantB />
    </LangProvider>
  );
}

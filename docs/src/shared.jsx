import React from "react";

const RAMUNE_BLUE = "#0A6FFF";
const RAMUNE_AQUA = "#3BB6FF";
const RAMUNE_DEEP = "#0B1B3A";
const RAMUNE_INK = "#0A1628";
const RAMUNE_GLASS = "rgba(59,182,255,0.12)";

const LangCtx = React.createContext({ lang: "en", setLang: () => {} });
const useLang = () => React.useContext(LangCtx);
const tr = (en, ja) => {
  const { lang } = useLang();
  return lang === "ja" ? ja : en;
};

function LangProvider({ children }) {
  const [lang, setLang] = React.useState("en");
  return <LangCtx.Provider value={{ lang, setLang }}>{children}</LangCtx.Provider>;
}

function Marble({ size = 60, style = {} }) {
  return (
    <div style={{
      width: size, height: size, borderRadius: "50%", position: "relative",
      background: `radial-gradient(circle at 32% 28%, #ffffff 0%, #c9ecff 18%, #5ab8ff 48%, #0a6fff 78%, #062f72 100%)`,
      boxShadow: `inset -4px -6px 12px rgba(6,47,114,.5), 0 8px 24px rgba(10,111,255,.35)`,
      ...style,
    }}>
      <div style={{
        position: "absolute", top: "12%", left: "18%", width: "28%", height: "22%",
        borderRadius: "50%", background: "rgba(255,255,255,.85)", filter: "blur(1px)",
      }} />
      <div style={{
        position: "absolute", top: "54%", left: "58%", width: "10%", height: "8%",
        borderRadius: "50%", background: "rgba(255,255,255,.6)",
      }} />
    </div>
  );
}

function RamuneBottle({ width = 160, height = 420, withMarble = true, fizzy = false }) {
  return (
    <svg width={width} height={height} viewBox="0 0 160 420" style={{ overflow: "visible" }}>
      <defs>
        <linearGradient id="glass" x1="0" x2="1" y1="0" y2="0">
          <stop offset="0" stopColor="#9bdaff" stopOpacity=".3"/>
          <stop offset=".4" stopColor="#3bb6ff" stopOpacity=".55"/>
          <stop offset=".6" stopColor="#1a8fff" stopOpacity=".6"/>
          <stop offset="1" stopColor="#0a4fb3" stopOpacity=".85"/>
        </linearGradient>
        <linearGradient id="highlight" x1="0" x2="0" y1="0" y2="1">
          <stop offset="0" stopColor="#fff" stopOpacity=".5"/>
          <stop offset="1" stopColor="#fff" stopOpacity="0"/>
        </linearGradient>
        <radialGradient id="marbleGrad" cx=".35" cy=".3">
          <stop offset="0" stopColor="#fff"/>
          <stop offset=".2" stopColor="#c9ecff"/>
          <stop offset=".5" stopColor="#5ab8ff"/>
          <stop offset=".85" stopColor="#0a6fff"/>
          <stop offset="1" stopColor="#062f72"/>
        </radialGradient>
      </defs>
      <path d="M 40 90 Q 40 70 55 60 L 55 40 Q 55 20 80 20 Q 105 20 105 40 L 105 60 Q 120 70 120 90 L 125 140 Q 130 170 130 210 L 130 360 Q 130 395 100 400 L 60 400 Q 30 395 30 360 L 30 210 Q 30 170 35 140 Z"
        fill="url(#glass)" stroke="rgba(255,255,255,.4)" strokeWidth="1"/>
      <path d="M 40 130 Q 80 120 120 130" fill="none" stroke="rgba(255,255,255,.3)" strokeWidth="1.5"/>
      <path d="M 40 148 Q 80 158 120 148" fill="none" stroke="rgba(255,255,255,.3)" strokeWidth="1.5"/>
      <path d="M 48 95 L 50 380" stroke="url(#highlight)" strokeWidth="3" strokeLinecap="round"/>
      {withMarble && <circle cx="80" cy="95" r="16" fill="url(#marbleGrad)"/>}
      {withMarble && <circle cx="74" cy="89" r="4" fill="rgba(255,255,255,.85)"/>}
      {fizzy && [...Array(14)].map((_, i) => {
        const x = 50 + (i * 13) % 60;
        const y = 160 + (i * 37) % 220;
        const r = 2 + (i % 3);
        return <circle key={i} cx={x} cy={y} r={r} fill="rgba(255,255,255,.55)"/>;
      })}
      <rect x="52" y="12" width="56" height="14" rx="2" fill="#c9d8e8" stroke="rgba(0,0,0,.1)"/>
      <rect x="52" y="12" width="56" height="4" fill="rgba(255,255,255,.5)"/>
    </svg>
  );
}

function LangToggle({ dark = false }) {
  const { lang, setLang } = useLang();
  const bg = dark ? "rgba(255,255,255,.08)" : "rgba(10,22,40,.06)";
  const fg = dark ? "rgba(255,255,255,.85)" : "rgba(10,22,40,.75)";
  const active = dark ? "#fff" : RAMUNE_INK;
  const activeBg = dark ? "rgba(255,255,255,.14)" : "#fff";
  return (
    <div style={{
      display: "inline-flex", padding: 3, borderRadius: 999, background: bg,
      fontSize: 12, fontWeight: 600, fontFamily: "Inter, sans-serif",
    }}>
      {["en", "ja"].map((l) => (
        <button key={l} onClick={() => setLang(l)} style={{
          border: "none", padding: "5px 12px", borderRadius: 999, cursor: "pointer",
          background: l === lang ? activeBg : "transparent",
          color: l === lang ? active : fg, fontWeight: 600,
          boxShadow: l === lang ? "0 1px 2px rgba(0,0,0,.08)" : "none",
          fontFamily: "inherit", fontSize: "inherit",
          letterSpacing: ".02em", textTransform: "uppercase",
        }}>{l}</button>
      ))}
    </div>
  );
}

function CodeBlock({ tabs, initial = 0, bg = "#0B1B3A", height }) {
  const [i, setI] = React.useState(initial);
  const t = tabs[i];
  return (
    <div style={{
      background: bg, borderRadius: 14, overflow: "hidden",
      boxShadow: "0 20px 60px -20px rgba(10,30,70,.4), 0 0 0 1px rgba(255,255,255,.06)",
      fontFamily: "JetBrains Mono, ui-monospace, monospace",
      border: "1px solid rgba(255,255,255,.08)",
    }}>
      <div style={{
        display: "flex", alignItems: "center", gap: 4,
        padding: "10px 14px", borderBottom: "1px solid rgba(255,255,255,.06)",
        background: "rgba(255,255,255,.02)",
      }}>
        <div style={{ display: "flex", gap: 6, marginRight: 14 }}>
          <span style={{ width: 11, height: 11, borderRadius: 6, background: "#ff5f57" }}/>
          <span style={{ width: 11, height: 11, borderRadius: 6, background: "#febc2e" }}/>
          <span style={{ width: 11, height: 11, borderRadius: 6, background: "#28c840" }}/>
        </div>
        {tabs.map((tab, idx) => (
          <button key={idx} onClick={() => setI(idx)} style={{
            border: "none", background: "transparent", cursor: "pointer",
            padding: "5px 11px", borderRadius: 6,
            fontFamily: "JetBrains Mono, monospace", fontSize: 11.5, fontWeight: 500,
            color: idx === i ? "#fff" : "rgba(255,255,255,.45)",
            borderBottom: idx === i ? `1.5px solid ${RAMUNE_AQUA}` : "1.5px solid transparent",
            marginBottom: -1,
          }}>{tab.name}</button>
        ))}
      </div>
      <pre style={{
        margin: 0, padding: "18px 22px", fontSize: 13, lineHeight: 1.65,
        color: "#e6f1ff", height, overflow: "auto",
        fontFamily: "inherit",
      }}><code dangerouslySetInnerHTML={{ __html: t.code }}/></pre>
    </div>
  );
}

const sy = {
  k: (s) => `<span style="color:#ff7ab6">${s}</span>`,
  s: (s) => `<span style="color:#a5e3ff">${s}</span>`,
  n: (s) => `<span style="color:#ffd17a">${s}</span>`,
  c: (s) => `<span style="color:#5e7a9b;font-style:italic">${s}</span>`,
  f: (s) => `<span style="color:#7ad9ff">${s}</span>`,
  t: (s) => `<span style="color:#b4a5ff">${s}</span>`,
  p: (s) => `<span style="color:#fff">${s}</span>`,
};

function BubbleField({ count = 24, blue = RAMUNE_AQUA }) {
  const bubbles = React.useMemo(() =>
    [...Array(count)].map((_, i) => ({
      x: (i * 37) % 100, y: (i * 53) % 100,
      size: 4 + ((i * 7) % 18),
      op: 0.1 + ((i * 11) % 40) / 100,
      dur: 6 + (i % 8),
      delay: -(i * 0.7),
    })), [count]);
  return (
    <div style={{ position: "absolute", inset: 0, pointerEvents: "none", overflow: "hidden" }}>
      {bubbles.map((b, i) => (
        <div key={i} style={{
          position: "absolute", left: `${b.x}%`, top: `${b.y}%`,
          width: b.size, height: b.size, borderRadius: "50%",
          border: `1px solid ${blue}`, opacity: b.op,
          background: `radial-gradient(circle at 30% 30%, rgba(255,255,255,.6), transparent 60%), ${blue}22`,
          animation: `ramuneFloat ${b.dur}s ease-in-out ${b.delay}s infinite`,
        }}/>
      ))}
    </div>
  );
}

export {
  RAMUNE_BLUE, RAMUNE_AQUA, RAMUNE_DEEP, RAMUNE_INK, RAMUNE_GLASS,
  LangCtx, LangProvider, useLang, tr,
  Marble, RamuneBottle, LangToggle, CodeBlock, sy, BubbleField,
};

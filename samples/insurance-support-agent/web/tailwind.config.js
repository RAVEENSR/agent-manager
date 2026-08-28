export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        brand: "var(--brand)",
        "brand-hi": "var(--brand-hi)",
        "brand-soft": "var(--brand-soft)",
        accent: "var(--accent)",
        surface: "var(--surface)",
        "border-subtle": "var(--border)",
        ink: "var(--text)",
        muted: "var(--muted)",
        bubble: "var(--bubble)",
        danger: "var(--danger)",
        ok: "var(--ok)",
      },
      borderRadius: { card: "14px" },
      maxWidth: { app: "1080px" },
    },
  },
  plugins: [],
};

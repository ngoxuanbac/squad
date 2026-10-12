// Text roles and spacing. Colours always come from the theme prop.
import type { PluginTheme } from "@getpaseo/plugin";
import { Platform, type TextStyle } from "react-native";

export type Colour = keyof PluginTheme["colors"];

export const SPACE = { xs: 2, sm: 4, md: 8, lg: 12, xl: 16, xxl: 24 } as const;
export const ICON = { sm: 14, md: 16 } as const;
export const RADIUS = 6;
/** One tree level's indent. */
export const INDENT = 18;

const MONO = Platform.select({ ios: "Menlo", default: "monospace" });

const ROLES = {
  /** Small muted labels: section headings, tags, times. */
  label: { fontSize: 12, lineHeight: 16, fontWeight: "400", colour: "foregroundMuted" },
  /** A section's heading. */
  section: { fontSize: 13, lineHeight: 18, fontWeight: "600", colour: "foreground" },
  /** A row's name. */
  title: { fontSize: 14, lineHeight: 20, fontWeight: "500", colour: "foreground" },
  /** A row's details. */
  meta: { fontSize: 13, lineHeight: 18, fontWeight: "400", colour: "foregroundMuted" },
  /** The header's counts. */
  stat: { fontSize: 18, lineHeight: 22, fontWeight: "600", colour: "foreground" },
  /** Log lines. */
  code: { fontSize: 12, lineHeight: 17, fontWeight: "400", colour: "foreground", fontFamily: MONO },
} as const satisfies Record<string, TextStyle & { colour: Colour }>;

export type Role = keyof typeof ROLES;

/** The style of a text role; `colour` swaps only its colour. */
export function text(theme: PluginTheme, role: Role, colour?: Colour): TextStyle {
  const { colour: base, ...style } = ROLES[role];
  return { ...style, color: theme.colors[colour ?? base] };
}

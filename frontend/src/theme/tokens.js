// Single source for visual values (Spec 4a). plugins/naive.js and the --wt-*
// CSS variables are both built from this file. Do not write color literals
// anywhere else: guards.test.js rejects them in .vue files.
//
// Text tokens are checked for 4.5:1 contrast on surface, bg, surfaceRaised
// and the highlighted row (firedTint over surface) in tokens.test.js. The
// dark primary is lighter than before (#7c6af7 → #8f80f8) so it is readable
// as link text, with dark text on primary buttons.
export const tokens = {
  dark: {
    bg: '#0f1117',
    surface: '#1a1d27',
    surfaceRaised: '#222634',
    border: '#2a2d3e',
    text: '#e2e8f0',
    textMuted: '#c3cad6',
    textSubtle: '#8b94a7',
    primary: '#8f80f8',
    primaryHover: '#a99cfa',
    primaryPressed: '#7c6af7',
    onPrimary: '#0f1117',
    primaryTint: 'rgba(143, 128, 248, 0.10)',
    up: '#36d399',
    upHover: '#4fdcac',
    upPressed: '#2bb583',
    down: '#f87272',
    fired: '#fbbd23',
    firedHover: '#fccb4f',
    firedPressed: '#e0a710',
    firedTint: 'rgba(251, 189, 35, 0.08)',
    armed: '#a99cfa',
    stale: '#fb923c',
    danger: '#f87272',
    dangerHover: '#fa8f8f',
    dangerPressed: '#e05a5a',
  },
  light: {
    bg: '#f8fafc',
    surface: '#ffffff',
    surfaceRaised: '#f5f7fa',
    border: '#e2e8f0',
    text: '#1e293b',
    textMuted: '#42536b',
    textSubtle: '#5f6b82',
    primary: '#6d58f0',
    primaryHover: '#5b46d1',
    primaryPressed: '#4c38b8',
    onPrimary: '#ffffff',
    primaryTint: 'rgba(109, 88, 240, 0.08)',
    up: '#15803d',
    upHover: '#166534',
    upPressed: '#14532d',
    down: '#b91c1c',
    fired: '#a16207',
    firedHover: '#854d0e',
    firedPressed: '#713f12',
    firedTint: 'rgba(161, 98, 7, 0.06)',
    armed: '#5b46d1',
    stale: '#c2410c',
    danger: '#b91c1c',
    dangerHover: '#991b1b',
    dangerPressed: '#7f1d1d',
  },
}

export const shared = {
  space: { xs: 4, sm: 8, md: 12, lg: 16, xl: 24, xxl: 32 },
  radius: { sm: 4, md: 8, lg: 12 },
  fontFamily: "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif",
}

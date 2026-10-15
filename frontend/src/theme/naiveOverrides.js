// Maps theme tokens onto Naive UI's common theme variables. Pure, so it can be
// tested without creating Naive's discrete API.
//
// baseColor is the text color Naive puts on primary/success/error buttons
// (button styles: textColorPrimary = baseColor), hence onPrimary.
export function naiveOverrides(t) {
  return {
    common: {
      bodyColor: t.bg,
      baseColor: t.onPrimary,
      cardColor: t.surface,
      modalColor: t.surface,
      popoverColor: t.surface,
      borderColor: t.border,
      dividerColor: t.border,
      primaryColor: t.primary,
      primaryColorHover: t.primaryHover,
      primaryColorPressed: t.primaryPressed,
      primaryColorSuppl: t.primaryHover,
      successColor: t.up,
      successColorHover: t.upHover,
      successColorPressed: t.upPressed,
      warningColor: t.fired,
      warningColorHover: t.firedHover,
      warningColorPressed: t.firedPressed,
      errorColor: t.danger,
      errorColorHover: t.dangerHover,
      errorColorPressed: t.dangerPressed,
      textColorBase: t.text,
      textColor1: t.text,
      textColor2: t.textMuted,
      textColor3: t.textSubtle,
    },
  }
}

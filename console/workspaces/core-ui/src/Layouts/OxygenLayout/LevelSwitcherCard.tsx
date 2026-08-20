import {
  Box,
  IconButton,
  ButtonBase,
  Menu,
  Typography,
  useTheme,
  type Theme,
} from "@wso2/oxygen-ui";
import { ChevronDown, ChevronRight, X } from "@wso2/oxygen-ui-icons-react";
import { type ElementType, type MouseEvent, type ReactNode } from "react";
import { Link, type LinkProps } from "react-router-dom";

/**
 * Adapts a target URL into the props that make a menu item render as a real
 * anchor (`<a>`) so clicking it opens the link — enabling middle-click /
 * open-in-new-tab and proper link semantics. `ComplexSelect.MenuItem` forwards
 * arbitrary props to the underlying MUI MenuItem but its prop type doesn't
 * include router Link props, so we pass them through a typed object.
 */
export const asLink = (
  to: LinkProps["to"],
): { component: ElementType; to: LinkProps["to"] } => ({
  component: Link,
  to,
});

/** Shared border treatment for the nav switchers — darkens on hover. */
export const hoverBorderSx = (theme: Theme) => ({
  border: `1px solid ${theme.vars?.palette.divider}`,
  borderRadius: theme.spacing(1),
  "&:hover": {
    border: `1px solid ${theme.vars?.palette.text.primary}`,
  },
});

const CARD_MAX_WIDTH = 220;

interface LevelSwitcherCardProps {
  /** Small caption shown above the value, e.g. "Projects" / "Agents". */
  label: string;
  /**
   * Present once a value is selected — renders the filled card (name, "go to"
   * link, and a close action that steps back up to the parent level).
   * Omit to render the empty placeholder chevron button instead.
   */
  selected?: {
    to: LinkProps["to"];
    goToLabel: string;
    /** The value row rendered below the label, e.g. the display name (+ badge). */
    content: ReactNode;
    closeLabel: string;
    onClose: () => void;
  };
  /** aria-label for the control that opens the switcher menu. */
  chevronLabel: string;
  anchorEl: HTMLElement | null;
  onOpenMenu: (event: MouseEvent<HTMLElement>) => void;
  onCloseMenu: () => void;
  /** Menu items rendered inside the switcher dropdown. */
  children: ReactNode;
}

/**
 * Org/Project/Agent switcher used in the top navigation. Has two states:
 * - No selection: a single chevron button that opens the picker menu.
 * - Selected: a card whose body links straight to that level's page, with a
 *   dedicated chevron button to switch to a different item and a close
 *   button to step back up to the parent level — each its own click target,
 *   rather than overlapping a single native select trigger.
 */
export function LevelSwitcherCard({
  label,
  selected,
  chevronLabel,
  anchorEl,
  onOpenMenu,
  onCloseMenu,
  children,
}: LevelSwitcherCardProps) {
  const theme = useTheme();
  const menuOpen = Boolean(anchorEl);

  if (!selected) {
    return (
      <>
        <IconButton
          onClick={onOpenMenu}
          size="small"
          aria-label={chevronLabel}
          aria-haspopup="menu"
          aria-expanded={menuOpen}
          sx={{
            "& .chevron-icon": {
              transform: menuOpen ? "rotate(90deg)" : "rotate(0deg)",
              transition: "transform 0.2s",
            },
            color: theme.vars?.palette.text.primary,
            p: theme.spacing(1, 1),
            ...hoverBorderSx(theme),
          }}
        >
          <ChevronRight size={20} className="chevron-icon" />
        </IconButton>
        <Menu anchorEl={anchorEl} open={menuOpen} onClose={onCloseMenu}>
          {children}
        </Menu>
      </>
    );
  }

  return (
    <Box
      position="relative"
      sx={{
        minWidth: 180,
        maxWidth: CARD_MAX_WIDTH,
        ...hoverBorderSx(theme),
      }}
    >
      <ButtonBase
        {...asLink(selected.to)}
        aria-label={selected.goToLabel}
        sx={{
          display: "flex",
          flexDirection: "column",
          alignItems: "flex-start",
          width: "100%",
          textAlign: "left",
          borderRadius: theme.spacing(1),
          p: theme.spacing(0.75, 4.5, 0.75, 1.5),
        }}
      >
        <Typography variant="caption" color="text.secondary">
          {label}
        </Typography>
        {selected.content}
      </ButtonBase>
      <IconButton
        size="small"
        aria-label={selected.closeLabel}
        sx={{
          position: "absolute",
          top: 2,
          right: 2,
          color: theme.vars?.palette.text.disabled,
        }}
        onClick={selected.onClose}
      >
        <X size={14} />
      </IconButton>
      <IconButton
        size="small"
        aria-label={chevronLabel}
        aria-haspopup="menu"
        aria-expanded={menuOpen}
        onClick={onOpenMenu}
        sx={{
          position: "absolute",
          bottom: 2,
          right: 2,
          color: theme.vars?.palette.text.secondary,
        }}
      >
        <ChevronDown size={18} />
      </IconButton>
      <Menu anchorEl={anchorEl} open={menuOpen} onClose={onCloseMenu}>
        {children}
      </Menu>
    </Box>
  );
}

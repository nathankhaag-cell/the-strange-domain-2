// Line icons from the approved mockups (24x24, stroke = currentColor).

import type { JSX } from "preact";

function Svg(props: { size?: number; children: JSX.Element | JSX.Element[] }) {
  const s = props.size ?? 18;
  return (
    <svg width={s} height={s} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
      {props.children}
    </svg>
  );
}

export const Lock = ({ size }: { size?: number }) => (
  <Svg size={size}>
    <rect x="4" y="11" width="16" height="10" />
    <path d="M8 11V7a4 4 0 0 1 8 0v4" />
  </Svg>
);

export const Speaker = () => (
  <Svg size={16}>
    <path d="M3 10v4h4l5 4V6L7 10H3z" />
    <path d="M16 9a4 4 0 0 1 0 6" />
    <path d="M19 6a8 8 0 0 1 0 12" />
  </Svg>
);

/** Abbot: crosier. */
export const Crosier = () => (
  <Svg>
    <path d="M12 22V9" />
    <path d="M12 9a4 4 0 1 1 4-4" />
    <path d="M9 14h6" />
  </Svg>
);

/** Bishop: mitre. */
export const Mitre = () => (
  <Svg>
    <path d="M6 21h12" />
    <path d="M7 21l-1-9 6-9 6 9-1 9" />
    <path d="M12 8v6" />
    <path d="M9.5 11h5" />
  </Svg>
);

/** Warden: key. */
export const Key = () => (
  <Svg>
    <circle cx="7.5" cy="12" r="3.5" />
    <path d="M11 12h10" />
    <path d="M17 12v3" />
    <path d="M20 12v2" />
  </Svg>
);

/** Brother / Sister: lamp. */
export const Lamp = () => (
  <Svg>
    <path d="M12 3c2 3 3 4.5 3 6.5a3 3 0 0 1-6 0C9 7.5 10 6 12 3z" />
    <path d="M8 14h8l-1 7H9z" />
  </Svg>
);

/** Postulant: candle. */
export const Candle = () => (
  <Svg>
    <path d="M12 2v4" />
    <rect x="9" y="8" width="6" height="13" />
  </Svg>
);

// Added for the phone layout and attachments (pending approval).

export const MenuIcon = () => (
  <Svg size={22}>
    <path d="M3 6h18" />
    <path d="M3 12h18" />
    <path d="M3 18h18" />
  </Svg>
);

export const Paperclip = () => (
  <Svg size={22}>
    <path d="M20 11l-8.5 8.5a5 5 0 0 1-7-7L13 4a3.5 3.5 0 0 1 5 5l-8.5 8.5a2 2 0 0 1-3-3L14 7" />
  </Svg>
);

export const FileIcon = () => (
  <Svg size={22}>
    <path d="M6 2h8l5 5v15H6z" />
    <path d="M14 2v5h5" />
  </Svg>
);

export function RoleIcon({ rank }: { rank: number }) {
  if (rank >= 1000) return <Crosier />;
  if (rank >= 500) return <Mitre />;
  if (rank >= 300) return <Key />;
  if (rank >= 100) return <Lamp />;
  return <Candle />;
}

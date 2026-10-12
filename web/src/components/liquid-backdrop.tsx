import { useId } from "react"

// A self-contained piece of atmospheric media. No external assets or requests.
export function LiquidBackdrop() {
  const id = useId().replaceAll(":", "")
  return <div className="liquid-backdrop" aria-hidden="true">
    <svg viewBox="0 0 1440 700" preserveAspectRatio="xMidYMid slice">
      <defs>
        <linearGradient id={`${id}-base`} x1="0" y1="0" x2="1" y2=".7">
          <stop stopColor="#091613" />
          <stop offset=".4" stopColor="#24392e" />
          <stop offset=".72" stopColor="#663121" />
          <stop offset="1" stopColor="#240a12" />
        </linearGradient>
        <linearGradient id={`${id}-glass`} x1=".08" y1=".2" x2=".86" y2=".8">
          <stop stopColor="#10231d" />
          <stop offset=".25" stopColor="#6caa84" />
          <stop offset=".4" stopColor="#b2dec0" />
          <stop offset=".53" stopColor="#d8cf8a" />
          <stop offset=".65" stopColor="#e8a64e" />
          <stop offset=".79" stopColor="#a13f2b" />
          <stop offset="1" stopColor="#370d1a" />
        </linearGradient>
        <linearGradient id={`${id}-rim`} x1="0" y1="0" x2="1" y2=".55">
          <stop stopColor="#a0e0ab" />
          <stop offset=".42" stopColor="#ecedbf" />
          <stop offset=".64" stopColor="#ffac2e" />
          <stop offset="1" stopColor="#a52d25" />
        </linearGradient>
        <filter id={`${id}-liquid`} x="-25%" y="-40%" width="150%" height="180%">
          <feTurbulence type="fractalNoise" baseFrequency=".003 .006" numOctaves="2" seed="14" result="noise" />
          <feDisplacementMap in="SourceGraphic" in2="noise" scale="105" xChannelSelector="R" yChannelSelector="G" />
          <feGaussianBlur stdDeviation="9" />
        </filter>
        <filter id={`${id}-soft`} x="-30%" y="-40%" width="160%" height="180%">
          <feGaussianBlur stdDeviation="30" />
        </filter>
        <filter id={`${id}-grain`}>
          <feTurbulence type="fractalNoise" baseFrequency=".72" numOctaves="3" stitchTiles="stitch" />
          <feColorMatrix type="saturate" values="0" />
        </filter>
      </defs>
      <rect width="1440" height="700" fill={`url(#${id}-base)`} />
      <g className="liquid-flow" filter={`url(#${id}-liquid)`}>
        <path d="M-240 550C-40 130 340-160 694 76S998 605 1710 185L1630 710C1250 1030 894 530 630 315S92 480-160 870Z" fill={`url(#${id}-glass)`} />
        <path d="M-150 558C152 66 392-51 676 137S1014 652 1585 273" fill="none" stroke={`url(#${id}-rim)`} strokeWidth="46" />
        <path d="M-170 670C146 143 374 55 656 223S963 722 1625 376" fill="none" stroke="#101b16" strokeOpacity=".88" strokeWidth="66" />
        <path d="M580-180C1100-78 667 208 1166 220S1478 70 1670-40" fill="none" stroke={`url(#${id}-glass)`} strokeWidth="166" />
      </g>
      <ellipse cx="1070" cy="650" rx="370" ry="150" fill="#a02d25" opacity=".4" filter={`url(#${id}-soft)`} />
      <rect width="1440" height="700" opacity=".055" filter={`url(#${id}-grain)`} className="liquid-grain" />
    </svg>
  </div>
}

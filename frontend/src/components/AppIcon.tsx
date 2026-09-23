type AppIconProps = {
  className?: string
}

export default function AppIcon({ className = 'w-6 h-6' }: AppIconProps) {
  return (
    <svg className={className} viewBox="0 0 1024 1024" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
      {/* 星芒 */}
      <path d="M800 170 L828 288 L946 316 L828 344 L800 462 L772 344 L654 316 L772 288 Z" fill="currentColor" />
      {/* EyvesCloud 云朵 */}
      <g fill="currentColor">
        <rect x="330" y="540" width="420" height="190" rx="95" />
        <circle cx="470" cy="540" r="120" />
        <circle cx="620" cy="480" r="150" />
        <circle cx="760" cy="560" r="110" />
      </g>
    </svg>
  )
}

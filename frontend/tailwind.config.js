/** @type {import('tailwindcss').Config} */
export default {
  darkMode: 'class',
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        // 设计系统：Cal.com（DESIGN.md）—— 白色画布 + 黑色主按钮 + 中性灰 + 蓝色仅作强调。
        // 浅色模式：600 为主按钮（#111111）；暗色模式：500 为主按钮（Cal 蓝 #3b82f6）。
        brand: {
          DEFAULT: '#111111',
          50: '#f8f9fa',
          100: '#f3f4f6',
          200: '#e5e7eb',
          300: '#d1d5db',
          400: '#60a5fa',
          500: '#3b82f6',
          600: '#111111',
          700: '#242424',
          800: '#1f1f1f',
          900: '#111111',
          950: '#0a0a0a',
        },
        // Cal 语义令牌（与 DESIGN.md 一一对应）
        canvas: '#ffffff',
        'surface-soft': '#f8f9fa',
        'surface-card': '#f5f5f5',
        'surface-strong': '#e5e7eb',
        'surface-dark': '#101010',
        'surface-dark-elevated': '#1a1a1a',
        ink: '#111111',
        body: '#374151',
        muted: '#6b7280',
        hairline: '#e5e7eb',
        'hairline-soft': '#f3f4f6',
        'on-dark-soft': '#a1a1aa',
        cal: {
          accent: '#3b82f6',
          success: '#10b981',
          warning: '#f59e0b',
          error: '#ef4444',
        },
        // 近黑：全局正文色（Cal ink）
        black: '#111111',
      },
      fontFamily: {
        sans: ['Inter', '-apple-system', 'BlinkMacSystemFont', 'Segoe UI', 'PingFang SC', 'Microsoft YaHei', 'sans-serif'],
      },
      boxShadow: {
        // Cal：克制的中性阴影（无彩色荧光）
        brand: '0 1px 2px rgba(17, 17, 17, 0.08)',
        'brand-hover': '0 2px 6px rgba(17, 17, 17, 0.12)',
        card: '0 1px 2px rgba(17, 17, 17, 0.04)',
        'card-hover': '0 4px 12px rgba(17, 17, 17, 0.08)',
      },
      transitionTimingFunction: {
        // 参考站 --motion-ease
        ease: 'cubic-bezier(.22, 1, .36, 1)',
      },
      keyframes: {
        'fade-up': {
          '0%': { opacity: '0', transform: 'translateY(14px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
        'header-arrive': {
          '0%': { opacity: '0', transform: 'translateY(-12px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
      },
      animation: {
        'fade-up': 'fade-up .6s cubic-bezier(.22, 1, .36, 1) both',
        'header-arrive': 'header-arrive .65s cubic-bezier(.22, 1, .36, 1) both',
      },
    },
  },
  plugins: [],
}

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
        // 品牌紫：对齐参考站设计系统（accent #7044ed / hover #5832cc / dark accent #ad8aff）
        brand: {
          DEFAULT: '#7044ed',
          50: '#f6f2ff',
          100: '#ede6fc',
          200: '#dccffb',
          300: '#c4b0f9',
          400: '#ad8aff',
          500: '#8a5cf0',
          600: '#7044ed',
          700: '#5b35cc',
          800: '#4a2ba6',
          900: '#3d2485',
          950: '#251653',
        },
        // ---- 设计系统令牌（Linear DESIGN.md，见项目根 DESIGN.md 与 docs/design-system.md）----
        // 新增 UI（页面/组件）优先使用下面这套语义令牌；存量页面保持现有紫调暗色不变。
        // canvas/surface 为近黑层次，hairline 为发丝描边，ink 为文字层级，accent 为强调色。
        canvas: '#010102',
        surface: {
          1: '#0f1011',
          2: '#141516',
          3: '#18191a',
          4: '#191a1b',
        },
        hairline: {
          DEFAULT: '#23252a',
          strong: '#34343a',
          tertiary: '#3e3e44',
        },
        ink: {
          DEFAULT: '#f7f8f8',
          muted: '#d0d6e0',
          subtle: '#8a8f98',
          tertiary: '#62666d',
        },
        accent: {
          DEFAULT: '#5e6ad2',
          hover: '#828fff',
          focus: '#5e69d1',
        },
        // 近黑紫：全局正文色（参考站 --text #242135），替代纯黑
        black: '#242135',
      },
      fontFamily: {
        sans: ['Inter', '-apple-system', 'BlinkMacSystemFont', 'Segoe UI', 'PingFang SC', 'Microsoft YaHei', 'sans-serif'],
      },
      boxShadow: {
        // 品牌柔光阴影（参考站 .button--primary 阴影体系）
        brand: '0 5px 13px rgba(112, 68, 237, 0.14)',
        'brand-hover': '0 8px 20px rgba(112, 68, 237, 0.22)',
        card: '0 15px 35px rgba(75, 52, 112, 0.08)',
        'card-hover': '0 18px 48px rgba(102, 71, 135, 0.10)',
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

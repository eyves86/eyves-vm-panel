// Applies the saved theme before React renders to avoid a flash of the
// wrong color scheme. Kept external so the CSP (script-src 'self') can
// forbid inline scripts.
(function () {
  var theme = localStorage.getItem('eyvescloud_theme')
  if (theme === 'dark' || (!theme && window.matchMedia('(prefers-color-scheme: dark)').matches)) {
    document.documentElement.classList.add('dark')
  }
})()

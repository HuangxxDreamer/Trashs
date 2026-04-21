/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{vue,js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        'industrial-blue': '#0a192f',
        'industrial-cyan': '#64ffda',
      }
    },
  },
  plugins: [],
}

/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ["./view/**/*.{go,html,js,jsx,ts,tsx,templ}"],
  theme: {
    extend: {},
  },
  plugins: [require("daisyui")],
};

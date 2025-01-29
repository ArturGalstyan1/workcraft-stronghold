/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ["./views/**/*.{go,html,js,jsx,ts,tsx,templ}"],
  theme: {
    extend: {},
  },
  plugins: [require("daisyui")],
};

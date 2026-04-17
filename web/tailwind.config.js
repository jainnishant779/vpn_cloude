/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        ink: "#1F2A37",
        canvas: "#F6F7F4",
        accent: "#0E8A7D",
        ember: "#D96C3E",
        gold: "#E5B64A"
      },
      boxShadow: {
        card: "0 18px 35px -20px rgba(31, 42, 55, 0.45)"
      },
      borderRadius: {
        xl2: "1.1rem"
      }
    }
  },
  plugins: []
};

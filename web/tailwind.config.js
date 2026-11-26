/** @type {import('tailwindcss').Config} */
export default {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{js,ts,jsx,tsx}'],
  theme: {
    extend: {
      colors: {
        calico: {
          orange: '#F08C18',
          orangeDeep: '#D9720B',
          orangeSoft: '#FFF4E4',
          orangeBorder: '#FAD4A1',
          orangeText: '#9E4A05',
          blue: '#155A96',
          blueMid: '#4B84B8',
          blueSoft: '#EDF5FC',
          blueBorder: '#C4DCF0',
          brown: '#6C4824',
          brownSoft: '#F6EFE8',
          brownBorder: '#DFCFC0',
          canvas: '#FBF9F6',
          subtle: '#F7F3EE',
          border: '#E9E1D6',
          ink: '#201B18',
        },
      },
      fontFamily: {
        sans: ['Inter', '-apple-system', 'BlinkMacSystemFont', 'PingFang SC', 'Segoe UI', 'sans-serif'],
        mono: ['JetBrains Mono', 'Menlo', 'Monaco', 'Consolas', 'monospace'],
      },
    },
  },
  plugins: [],
};

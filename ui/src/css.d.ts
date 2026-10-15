/** Vite 会打包这些样式。没有这份声明时，`import './x.css'` 会报 TS2882。 */
declare module '*.css' {}

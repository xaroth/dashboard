// Class names are checked against the CSS by renderer/pages.test.ts.
declare module "*.module.css" {
  const classes: { readonly [name: string]: string | undefined };
  export default classes;
}

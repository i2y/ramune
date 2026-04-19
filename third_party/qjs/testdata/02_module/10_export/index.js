export const sayHello = msg => {
  console.log(`[${msg}] JS says hello`);
}

export const sayHi = msg => {
  console.log(`[${msg}] JS says hi`);
}

export const init = () => {
  registerJsFunction('sayHello');
  registerJsFunction('sayHi');
}

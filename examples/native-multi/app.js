// Multi-file native extension: geometry.ts imports from math.ts
const { square, cube } = require('native:math');
const { circleArea, sphereVolume } = require('native:geometry');

console.log("square(5) =", square(5));
console.log("cube(3) =", cube(3));
console.log("circleArea(5) =", circleArea(5));
console.log("sphereVolume(3) =", sphereVolume(3));

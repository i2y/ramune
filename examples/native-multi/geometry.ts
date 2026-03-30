import { square } from './math';

export function circleArea(radius: number): number {
  return Math.PI * square(radius);
}

export function sphereVolume(radius: number): number {
  return (4 / 3) * Math.PI * radius * radius * radius;
}

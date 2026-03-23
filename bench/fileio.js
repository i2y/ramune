var fs = require('fs');
var os = require('os');
var path = os.tmpdir() + '/esffi_bench_io.txt';
for (var i = 0; i < 100; i++) {
    fs.writeFileSync(path, 'line ' + i + '\n');
    fs.readFileSync(path, 'utf8');
}
fs.rmSync(path);
console.log("done");

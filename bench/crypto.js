var crypto = require('crypto');
var result = '';
for (var i = 0; i < 1000; i++) {
    result = crypto.createHash('sha256').update('hello' + i).digest('hex');
}
console.log(result);

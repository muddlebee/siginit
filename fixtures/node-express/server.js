const express = require('express');
const app = express();
const port = process.env.PORT || 3000;

app.use(express.json());

app.get('/', (req, res) => {
  res.json({ status: 'ok', ts: Date.now() });
});

app.get('/users', (req, res) => {
  res.json([{ id: 1, name: 'Alice' }, { id: 2, name: 'Bob' }]);
});

app.post('/orders', (req, res) => {
  const { item } = req.body;
  if (!item) return res.status(400).json({ error: 'item required' });
  res.status(201).json({ id: Math.random().toString(36).slice(2), item });
});

app.listen(port, () => {
  console.log(`demo-express-app listening on :${port}`);
});

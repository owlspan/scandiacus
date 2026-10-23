// A to-do list with its interface in HTML and CSS.
//
// The list lives here, in Scandiacus, and is saved to todos.json beside the
// program. The page only draws it: it sends what the user did with
// scandiacus.send, and gets the whole list back after every change.
//
//   scandiacus run examples/ui/todo.scd

struct Todo {
    id: int
    text: str
    done: bool
}

const page = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<style>
  :root {
    --bg: #0f1115; --card: #171a21; --line: #262b36; --text: #e8eaf0;
    --dim: #8a91a3; --accent: #6d8cff; --accent-soft: rgba(109, 140, 255, .14);
  }
  * { box-sizing: border-box; }
  html, body { margin: 0; height: 100%; }
  body {
    background: radial-gradient(1200px 600px at 10% -10%, #1b2133 0%, var(--bg) 60%);
    color: var(--text); font: 15px/1.45 "Segoe UI Variable Text", "Segoe UI", system-ui, sans-serif;
    display: flex; justify-content: center; padding: 48px 20px; user-select: none;
  }
  main { width: 100%; max-width: 520px; }
  h1 { font-size: 28px; font-weight: 650; letter-spacing: -.02em; margin: 0 0 4px; }
  .sub { color: var(--dim); margin: 0 0 24px; }
  form { display: flex; gap: 8px; margin-bottom: 16px; }
  input[type=text] {
    flex: 1; background: var(--card); border: 1px solid var(--line); color: var(--text);
    border-radius: 10px; padding: 11px 14px; font: inherit; outline: none; user-select: text;
    transition: border-color .15s, box-shadow .15s;
  }
  input[type=text]:focus { border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-soft); }
  button {
    border: 0; border-radius: 10px; font: inherit; cursor: pointer; color: var(--text);
    background: var(--card); padding: 0 14px; transition: background .15s, color .15s;
  }
  .add { background: var(--accent); color: #fff; font-weight: 600; padding: 0 18px; }
  .add:hover { background: #8299ff; }
  .tabs { display: flex; gap: 4px; margin-bottom: 12px; }
  .tabs button { padding: 6px 12px; color: var(--dim); background: transparent; border-radius: 8px; }
  .tabs button.on { background: var(--accent-soft); color: var(--accent); }
  ul { list-style: none; margin: 0; padding: 0; background: var(--card);
       border: 1px solid var(--line); border-radius: 14px; overflow: hidden; }
  li { display: flex; align-items: center; gap: 12px; padding: 12px 14px; border-top: 1px solid var(--line); }
  li:first-child { border-top: 0; }
  li:hover { background: #1b1f28; }
  .box {
    width: 20px; height: 20px; border-radius: 6px; border: 2px solid #3a4152; flex: none;
    display: grid; place-items: center; cursor: pointer; transition: all .15s;
  }
  .done .box { background: var(--accent); border-color: var(--accent); }
  .done .box::after { content: ""; width: 5px; height: 10px; border: solid #fff;
                      border-width: 0 2px 2px 0; transform: translateY(-1px) rotate(45deg); }
  .text { flex: 1; overflow-wrap: anywhere; }
  .done .text { color: var(--dim); text-decoration: line-through; }
  .del { opacity: 0; background: transparent; color: var(--dim); padding: 4px 8px; }
  li:hover .del { opacity: 1; }
  .del:hover { color: #ff6b6b; }
  .empty { color: var(--dim); text-align: center; padding: 28px; }
  footer { display: flex; justify-content: space-between; color: var(--dim); font-size: 13px; margin-top: 12px; }
  footer button { background: transparent; color: var(--dim); padding: 0; }
  footer button:hover { color: var(--text); }
</style>
</head>
<body>
<main>
  <h1>To do</h1>
  <p class="sub">Kept by a Scandiacus program, drawn by this page.</p>
  <form id="form">
    <input id="new" type="text" placeholder="What needs doing?" autocomplete="off" autofocus>
    <button class="add">Add</button>
  </form>
  <div class="tabs">
    <button data-show="all" class="on">All</button>
    <button data-show="open">Open</button>
    <button data-show="done">Done</button>
  </div>
  <ul id="list"></ul>
  <footer><span id="left"></span><button id="clear">Clear finished</button></footer>
</main>
<script>
  let todos = [], show = "all";
  const $ = (id) => document.getElementById(id);

  function draw() {
    const list = $("list");
    list.innerHTML = "";
    const seen = todos.filter(t => show === "all" || (show === "done") === t.done);
    for (const t of seen) {
      const li = document.createElement("li");
      li.className = t.done ? "done" : "";
      li.innerHTML = '<span class="box"></span><span class="text"></span><button class="del">Delete</button>';
      li.querySelector(".text").textContent = t.text;
      li.querySelector(".box").onclick = () => scandiacus.send({ op: "toggle", id: t.id });
      li.querySelector(".del").onclick = () => scandiacus.send({ op: "delete", id: t.id });
      list.appendChild(li);
    }
    if (seen.length === 0) list.innerHTML = '<li class="empty">Nothing here.</li>';
    const open = todos.filter(t => !t.done).length;
    $("left").textContent = open === 1 ? "1 thing left" : open + " things left";
  }

  scandiacus.onmessage = (text) => { todos = JSON.parse(text); draw(); };
  $("form").onsubmit = (e) => {
    e.preventDefault();
    const text = $("new").value.trim();
    if (text) scandiacus.send({ op: "add", text });
    $("new").value = "";
  };
  $("clear").onclick = () => scandiacus.send({ op: "clear" });
  for (const b of document.querySelectorAll(".tabs button")) {
    b.onclick = () => {
      document.querySelectorAll(".tabs button").forEach(x => x.classList.remove("on"));
      b.classList.add("on");
      show = b.dataset.show;
      draw();
    };
  }
  window.addEventListener("load", () => scandiacus.send({ op: "ready" }));
</script>
</body>
</html>`

const saved = "todos.json"

let todos: []Todo = json.decodeOr(os.file.readOr(saved, "[]"), [])
let next = 1
for t in todos {
    if t.id >= next { next = t.id + 1 }
}

let app = must(ui.open("To do", 560, 640))
ui.html(app, page)

while ui.poll(app) {
    let changed = false
    for m in ui.messages(app) {
        let op = json.get(m, "op")
        let id = json.int(m, "id")
        if op == "add" {
            push(todos, Todo{id: next, text: json.get(m, "text"), done: false})
            next += 1
            changed = true
        } else if op == "toggle" {
            for i in 0..len(todos) {
                if todos[i].id == id { todos[i].done = !todos[i].done }
            }
            changed = true
        } else if op == "delete" {
            todos = filter(todos, fn(t: Todo) -> bool { return t.id != id })
            changed = true
        } else if op == "clear" {
            todos = filter(todos, fn(t: Todo) -> bool { return !t.done })
            changed = true
        }
        if changed || op == "ready" {
            ui.send(app, json.encode(todos))
        }
    }
    if changed {
        os.file.write(saved, json.encode(todos))
    }
}
ui.close(app)

" Building and running a .vl file from Vim.
"
"   :make      veyl build on this file; errors land in the quickfix list
"   F5         save and veyl run this file
"
" veyl reports errors as file:line:col: message, which is all the
" errorformat needs. A drive letter's colon is part of %f on Windows.
setlocal makeprg=veyl\ build\ %
setlocal errorformat=%f:%l:%c:\ %m
nnoremap <buffer> <F5> :update<CR>:!veyl run "%"<CR>

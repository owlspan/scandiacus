" Building and running a .scd file from Vim.
"
"   :make      scandiacus build on this file; errors land in the quickfix list
"   F5         save and scandiacus run this file
"
" scandiacus reports errors as file:line:col: message, which is all the
" errorformat needs. A drive letter's colon is part of %f on Windows.
setlocal makeprg=scandiacus\ build\ %
setlocal errorformat=%f:%l:%c:\ %m
nnoremap <buffer> <F5> :update<CR>:!scandiacus run "%"<CR>

// re matches as Go's regexp does, captures included, and stays
// linear: loops whose body can match nothing, nested lazy loops, and
// a pattern that is exponential for most backtracking engines.
print("[" + re.find(`(a*)*`, "aab") + "] " + join(re.groups(`(a*)*`, "aab"), "|"))
print("[" + re.find(`(a?a*?)*?$`, "  a") + "] " + join(re.groups(`(a?a*?)*?$`, "  a"), "|"))
print("[" + re.find(`bb*|(a?a*?)*?$`, "  a") + "] " + join(re.groups(`bb*|(a?a*?)*?$`, "  a"), "|"))
print("[" + re.find(`\S+(\D*?)*( {1,1}b?|a+?\s{1,3})*?`, "ab 1a1") + "] " + join(re.groups(`\S+(\D*?)*( {1,1}b?|a+?\s{1,3})*?`, "ab 1a1"), "|"))
print("[" + re.find(`([^a]*b?[^a]?)+`, "_b1a") + "] " + join(re.groups(`([^a]*b?[^a]?)+`, "_b1a"), "|"))
print("[" + re.find(`(.*?)+a`, "_babab1  ") + "] " + join(re.groups(`(.*?)+a`, "_babab1  "), "|"))
print("[" + re.find(`(b?(b*?[ab]*)+){0,2} ?`, "b ") + "] " + join(re.groups(`(b?(b*?[ab]*)+){0,2} ?`, "b "), "|"))
print("[" + re.find(`(\w?)*_`, " ab_") + "] " + join(re.groups(`(\w?)*_`, " ab_"), "|"))
print("[" + re.find(`(a|ab)(c|bcd)(d*)`, "abcd") + "] " + join(re.groups(`(a|ab)(c|bcd)(d*)`, "abcd"), "|"))
print("[" + re.find(`(a+)+b`, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaac") + "] " + join(re.groups(`(a+)+b`, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaac"), "|"))
print(join(re.split(`1*`, "b"), "|") + " (" + str(len(re.split(`1*`, "b"))) + ")")
print(join(re.split(`\w`, ""), "|") + " (" + str(len(re.split(`\w`, ""))) + ")")
print(join(re.split(`,`, "a,b,"), "|") + " (" + str(len(re.split(`,`, "a,b,"))) + ")")
print(join(re.split(`x*`, "axbx"), "|") + " (" + str(len(re.split(`x*`, "axbx"))) + ")")

#ifndef ANYDOC_H
#define ANYDOC_H

#ifdef __cplusplus
extern "C" {
#endif

char* anydoc_convert(const char* path);
void anydoc_free(char* s);

#ifdef __cplusplus
}
#endif

#endif // ANYDOC_H

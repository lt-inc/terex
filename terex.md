# terex proj
## FE
Next.js react typescript taiwindcss
### main page
1. Страница с боковой панелью навигации, на ней двевовидная навигация
2. две большие группы admin (здесь все страницы ниже) и other services (ссылка на остальные сабдомены).
3. сбоку заглушка когда ниче не открыто
4. сверху хедер низу футтер пока пустые
### upload video page
1. влевом верхнем углу кнопка add video
2. вправом верхем углу кнопка upload all videos
3. нажатие на add video создает карточку для заполнения в ней следующие поля:
    3.1. Youtube URL (text box)
    3.2. Make vertical? (slide button) 
    3.3. Add subtitles? (slide button)
    3.4. Slice video? (slide button)
    3.5. Add background? (if 3.2. == false то не активный slide button)
    3.6. Circle (slide button) Rectangle
    3.7. Add banner? (slide button)
4. Если 3.7. == true, добавить кнопку add banner которая внутри карточки создает еще одну со следующими полями
    4.1. YouTube account (dropbox выбор один или несколько)
    4.2. TikTok account (dropbox выбор один или несколько)
    4.3. Instagram account (dropbox выбор один или несколько)
    4.4. Для каждого из платформ поле <platform_name> publish date time с 3 полями (дата время и шаг)
    4.5. Banner (dropbox)
### create account page
аналогично upload video page только поля
1. Platform (dopbox)
2. Name
3. url
4. auth file (drag and drop or chose)
4. note

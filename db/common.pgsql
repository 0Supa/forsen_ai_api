insert into permissions ("twitch_login", "twitch_user_id", "status", "permission") 
values 
    ('shirogopher', 82054454, 1, 2),
    ('shirogopher', 82054454, 1, 1),
    ('shirogopher', 82054454, 1, 0);

delete from permissions where lower("twitch_login") = 'shirogopher' and "permission" = 1;

delete from msg_queue;

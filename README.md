# JP translator

## install
```
go get github.com/tacogips/ej
```

## usage

get google translate api key from gcp developer console,
and set it into environment variable named `EJ_GOOGLE_TRANS_API_KEY`.

https://cloud.google.com/translate/docs/getting-started

you can use rapid www.wordsapi.com API to show definition alongside of translation result.
https://www.wordsapi.com/pricing

wordsapi is hosted on RapidAPI.
https://docs.rapidapi.com/v1.0/docs/basics-creating-a-project


```
export EJ_GOOGLE_TRANS_API_KEY="your_api_key"
# (optional) RAPID API KEY(to use wordsapi)
export EJ_RAPID_API_KEY="your_key"

```

and ej command with original sentence

```
> ej i am a man
i am a man
私は男です

> ej 我是一个男人
我是一个男人
私は男です

# translate to english if input word detected as japanese
> ej どすこい
Sumo exclamation

# show all caches json
> ej -c -json
[{"input":"脆弱a","translated":"Vulnerable"},{"input":"脆弱性","translated":"Vulnerability"}]
```

# Disclaimer
This tool uses google translation api that not cost-free.
Heavy using leads you to bankruptcy.
At your own risk and wallet.

https://cloud.google.com/translate/pricing
https://www.wordsapi.com/pricing
